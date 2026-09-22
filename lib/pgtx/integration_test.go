//go:build integration

package pgtx_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/jackc/pgx/v5"
)

// Environment: GO_TEST_PG_DSN — the module's connection as
// apibot; GO_TEST_ADMIN_DSN — used ONLY to mint a live session with
// api.login(admin, <password of the DSN>), the way AuthServer would. Both are
// read from the environment, never from code.
func env(t *testing.T) (pool string, mint func(t *testing.T) string) {
	t.Helper()
	dsn, admin := os.Getenv("GO_TEST_PG_DSN"), os.Getenv("GO_TEST_ADMIN_DSN")
	if dsn == "" || admin == "" {
		t.Skip("GO_TEST_PG_DSN / GO_TEST_ADMIN_DSN not set")
	}
	cfg, err := pgx.ParseConfig(admin)
	if err != nil {
		t.Fatal(err)
	}
	mint = func(t *testing.T) string {
		t.Helper()
		conn, err := pgx.Connect(context.Background(), admin)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(context.Background())
		var session string
		if err := conn.QueryRow(context.Background(), "SELECT session FROM api.login($1, $2, 'go-platform-test', '127.0.0.1')", cfg.User, cfg.Password).Scan(&session); err != nil {
			t.Fatalf("mint session: %v", err)
		}
		t.Cleanup(func() {
			c, err := pgx.Connect(context.Background(), admin)
			if err == nil {
				_, _ = c.Exec(context.Background(), "SELECT api.signout($1)", session)
				c.Close(context.Background())
			}
		})
		return session
	}
	return dsn, mint
}

func runner(t *testing.T, dsn string) *pgtx.Runner {
	t.Helper()
	pool, err := pgtx.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &pgtx.Runner{Pool: pool}
}

func TestIntegration_AuthorizedRequestCommits(t *testing.T) {
	dsn, mint := env(t)
	r := runner(t, dsn)
	var n int64
	err := r.Do(context.Background(), pgtx.Session{Code: mint(t), Agent: "go-test", Host: "127.0.0.1"}, nil, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT api.count_client()").Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("count_client = %d", n)
	}
}

func TestIntegration_BadSessionIs401(t *testing.T) {
	dsn, _ := env(t)
	r := runner(t, dsn)
	err := r.Do(context.Background(), pgtx.Session{Code: "0000000000000000000000000000000000000000"}, nil, func(ctx context.Context, tx pgx.Tx) error {
		t.Fatal("handler must not run")
		return nil
	})
	var p *problem.Problem
	if !errors.As(err, &p) || p.Status != 401 {
		t.Fatalf("got %v", err)
	}
}

func TestIntegration_CatalogueErrorBecomesProblem(t *testing.T) {
	dsn, mint := env(t)
	r := runner(t, dsn)
	err := r.Do(context.Background(), pgtx.Session{Code: mint(t)}, nil, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "SELECT api.execute_object_action('00000000-0000-4000-b000-00000000dead'::uuid, 'enable')")
		return err
	})
	var p *problem.Problem
	if !errors.As(err, &p) {
		t.Fatalf("got %v", err)
	}
	if p.Code == nil || p.Status < 400 || p.Status >= 500 || p.Title == "" || p.Detail == "" {
		t.Fatalf("problem %+v", p)
	}
	t.Logf("catalogue: %s %d %q / %q", *p.Code, p.Status, p.Title, p.Detail)
}

func TestIntegration_TwoSessionsDoNotLeakIntoEachOther(t *testing.T) {
	dsn, mint := env(t)
	r := runner(t, dsn)
	a, b := mint(t), mint(t)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 20; i++ {
		for _, code := range []string{a, b} {
			wg.Add(1)
			go func(code string) {
				defer wg.Done()
				errs <- r.Do(context.Background(), pgtx.Session{Code: code}, nil, func(ctx context.Context, tx pgx.Tx) error {
					time.Sleep(5 * time.Millisecond)
					var seen string
					if err := tx.QueryRow(ctx, "SELECT current_setting('current.session', true)").Scan(&seen); err != nil {
						return err
					}
					if seen != code {
						return errors.New("transaction sees another session: " + seen[:8] + " ≠ " + code[:8])
					}
					return nil
				})
			}(code)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestIntegration_ConnectionIsResetAfterRelease(t *testing.T) {
	dsn, mint := env(t)
	r := runner(t, dsn)
	if err := r.Do(context.Background(), pgtx.Session{Code: mint(t)}, nil, func(ctx context.Context, tx pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	// every pooled connection must come back without a session
	for i := 0; i < 5; i++ {
		var seen *string
		if err := r.Pool.QueryRow(context.Background(), "SELECT NULLIF(current_setting('current.session', true), '')").Scan(&seen); err != nil {
			t.Fatal(err)
		}
		if seen != nil {
			t.Fatalf("connection still carries session %s… after release", (*seen)[:8])
		}
	}
}

func TestIntegration_Detect(t *testing.T) {
	dsn, _ := env(t)
	r := runner(t, dsn)
	if err := r.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Logf("features: %+v", r.Features)
}

func TestIntegration_LogRequestWhenPatched(t *testing.T) {
	dsn, mint := env(t)
	r := runner(t, dsn)
	if err := r.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.Features.LogRequest {
		t.Skip("api.log_request not in this database (gateway patch not applied)")
	}
	req := &pgtx.Request{Method: "GET", Path: "/api/v2/clients", RequestID: "6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d11"}
	if err := r.Do(context.Background(), pgtx.Session{Code: mint(t)}, req, func(ctx context.Context, tx pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if req.LogID == 0 {
		t.Fatal("LogID not set")
	}
}

func TestIntegration_AuthorizeLocalIsolatesWhenPatched(t *testing.T) {
	dsn, mint := env(t)
	r := runner(t, dsn)
	if err := r.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.Features.AuthorizeLocal {
		t.Skip("api.authorize_local not in this database (gateway patch not applied)")
	}
	code := mint(t)
	var seen *string
	err := r.Do(context.Background(), pgtx.Session{Code: code}, nil, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT NULLIF(current_setting('current.session', true), '')").Scan(&seen)
	})
	if err != nil || seen == nil || *seen != code {
		t.Fatalf("inside tx: %v %v", err, seen)
	}
}

// journalled reads the db.api_log row Do wrote, under the same session.
func journalled(t *testing.T, r *pgtx.Runner, code string, id int64) (username *string, session *string, status int, path string) {
	t.Helper()
	u, s, req, p := journalledRow(t, r, code, id)
	return u, s, req.Status, p
}

// requestOf is the _request object api.log_request writes into the row.
type requestOf struct {
	Status int    `json:"status"`
	Method string `json:"method"`
	Error  string `json:"error"` // the catalogue code of a refusal (1.2.24), absent otherwise
}

func journalledRow(t *testing.T, r *pgtx.Runner, code string, id int64) (username *string, session *string, request requestOf, path string) {
	t.Helper()
	err := r.Do(context.Background(), pgtx.Session{Code: code}, nil, func(ctx context.Context, tx pgx.Tx) error {
		var body []byte
		if err := tx.QueryRow(ctx, "SELECT username, session, path, json->'_request' FROM api.get_log($1::bigint)", id).Scan(&username, &session, &path, &body); err != nil {
			return err
		}
		return json.Unmarshal(body, &request)
	})
	if err != nil {
		t.Fatalf("read api_log %d: %v", id, err)
	}
	return username, session, request, path
}

func patched(t *testing.T) (*pgtx.Runner, func(t *testing.T) string) {
	t.Helper()
	dsn, mint := env(t)
	r := runner(t, dsn)
	if err := r.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.Features.LogRequest || !r.Features.AuthorizeLocal {
		t.Skip("gateway patch not applied (api.log_request / api.authorize_local)")
	}
	return r, mint
}

// A refusal decided by the handler (404 of a foreign object, T142) leaves
// a row with the status and the session's user, and none of the handler's
// work: the audit is the only thing committed.
func TestIntegration_HandlerRefusalIsJournalledUnderTheSession(t *testing.T) {
	r, mint := patched(t)
	code := mint(t)
	req := &pgtx.Request{Method: "GET", Path: "/api/v2/vessels/dead", RequestID: "6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d12"}
	err := r.Do(context.Background(), pgtx.Session{Code: code}, req, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT api.set_session_oper_date('2026-01-02T03:04:05Z'::timestamptz)"); err != nil { // work before the refusal
			return err
		}
		return problem.New(404, "not-found", "Not found", "")
	})
	var p *problem.Problem
	if !errors.As(err, &p) || p.Status != 404 {
		t.Fatalf("got %v", err)
	}
	if req.LogID == 0 {
		t.Fatal("refusal not journalled")
	}
	username, session, request, path := journalledRow(t, r, code, req.LogID)
	if username == nil || session == nil || *session != code || request.Status != 404 || path != req.Path {
		t.Fatalf("row: user %v session %v status %d path %s", username, session, request.Status, path)
	}
	// a module-side refusal has no catalogue code: the row carries none
	if request.Error != "" {
		t.Fatalf("row: error %q for a refusal without a catalogue code", request.Error)
	}
	var operDate *time.Time
	if err := r.Do(context.Background(), pgtx.Session{Code: code}, nil, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT api.oper_date()").Scan(&operDate)
	}); err != nil || operDate != nil {
		t.Fatalf("the handler's work survived the refusal: %v %v", err, operDate)
	}
}

// A refusal raised by the database (catalogue error, aborted transaction)
// is journalled the same way: rolled back to the savepoint, then written.
func TestIntegration_CatalogueRefusalIsJournalled(t *testing.T) {
	r, mint := patched(t)
	code := mint(t)
	req := &pgtx.Request{Method: "POST", Path: "/api/v2/objects/dead/actions/enable", Payload: []byte(`{"x":1}`)}
	err := r.Do(context.Background(), pgtx.Session{Code: code}, req, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "SELECT api.execute_object_action('00000000-0000-4000-b000-00000000dead'::uuid, 'enable')")
		return err
	})
	var p *problem.Problem
	if !errors.As(err, &p) || p.Status < 400 || p.Status >= 500 {
		t.Fatalf("got %v", err)
	}
	if req.LogID == 0 {
		t.Fatal("refusal not journalled")
	}
	_, session, request, _ := journalledRow(t, r, code, req.LogID)
	if session == nil || *session != code || request.Status != p.Status {
		t.Fatalf("row: session %v status %d ≠ %d", session, request.Status, p.Status)
	}
	// db-platform 1.2.24: the catalogue code of the refusal is in the row
	// (_request.error); an older api.log_request has no place for it
	if p.Code == nil {
		t.Fatalf("catalogue refusal without a code: %v", p)
	}
	switch {
	case r.Features.LogRequestErr && request.Error != *p.Code:
		t.Fatalf("row: error %q, the refusal was %s", request.Error, *p.Code)
	case !r.Features.LogRequestErr && request.Error != "":
		t.Fatalf("row: error %q on a six-parameter api.log_request", request.Error)
	case !r.Features.LogRequestErr:
		t.Logf("api.log_request has six parameters here: the code %s is not journalled", *p.Code)
	}
}

// A session the database refuses (401) is journalled without a session.
func TestIntegration_RefusedSessionIsJournalledWithoutOne(t *testing.T) {
	r, mint := patched(t)
	req := &pgtx.Request{Method: "GET", Path: "/api/v2/me"}
	err := r.Do(context.Background(), pgtx.Session{Code: "0000000000000000000000000000000000000000"}, req, func(ctx context.Context, tx pgx.Tx) error { return nil })
	var p *problem.Problem
	if !errors.As(err, &p) || p.Status != 401 {
		t.Fatalf("got %v", err)
	}
	if req.LogID == 0 {
		t.Fatal("refusal not journalled")
	}
	if username, session, status, _ := journalled(t, r, mint(t), req.LogID); username != nil || session != nil || status != 401 {
		t.Fatalf("row: user %v session %v status %d", username, session, status)
	}
}

// A session the database refuses by raising (IP table, locked user, expired
// password — an aborted transaction, not a `false`) is journalled too,
// without a session, with the status of the catalogue code.
func TestIntegration_RaisedRefusalIsJournalled(t *testing.T) {
	r, mint := patched(t)
	admin := os.Getenv("GO_TEST_ADMIN_DSN")
	conn, err := pgx.Connect(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	cfg, _ := pgx.ParseConfig(admin)
	var own, uid string
	if err := conn.QueryRow(context.Background(), "SELECT session FROM api.login($1, $2, 'go-platform-test', '127.0.0.1')", cfg.User, cfg.Password).Scan(&own); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "SELECT api.signout($1)", own) })
	if err := conn.QueryRow(context.Background(), "SELECT api.current_userid()").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	// deny one documentation address for this user; the suite's own 127.0.0.1 stays allowed
	if _, err := conn.Exec(context.Background(), "SELECT api.set_user_iptable($1, 'D', '203.0.113.9')", uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "SELECT api.set_user_iptable($1, 'D', '')", uid) })
	code := mint(t)
	req := &pgtx.Request{Method: "GET", Path: "/api/v2/me"}
	err = r.Do(context.Background(), pgtx.Session{Code: code, Host: "203.0.113.9"}, req, func(ctx context.Context, tx pgx.Tx) error {
		t.Fatal("handler must not run")
		return nil
	})
	var p *problem.Problem
	if !errors.As(err, &p) || p.Status < 400 || p.Status >= 500 { // ERR-400-044 in db-platform 1.2.23: the code's group, not 401
		t.Fatalf("got %v", err)
	}
	if req.LogID == 0 {
		t.Fatal("refusal not journalled")
	}
	username, session, request, _ := journalledRow(t, r, code, req.LogID)
	if username != nil || session != nil || request.Status != p.Status {
		t.Fatalf("row: user %v session %v status %d ≠ %d", username, session, request.Status, p.Status)
	}
	// the fresh transaction journals the raised code too (1.2.24)
	if r.Features.LogRequestErr && (p.Code == nil || request.Error != *p.Code) {
		t.Fatalf("row: error %q, the refusal was %v", request.Error, p.Code)
	}
}
