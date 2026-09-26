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
	"github.com/apostoldevel/go-platform/lib/pgtx/pgtxtest"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/jackc/pgx/v5"
)

// The daemon road (db-platform 1.2.31): GO_TEST_PG_DSN as the daemon role,
// GO_TEST_ADMIN_DSN to mint a session and a token the database issued,
// GO_TEST_AUDIENCE the client id the token is issued for (a web front-end's).
// Skipped under a role that does not take the road.

func daemonRoad(t *testing.T) (*pgtx.Runner, func(t *testing.T) pgtx.Session) {
	t.Helper()
	dsn, admin, aud := os.Getenv("GO_TEST_PG_DSN"), os.Getenv("GO_TEST_ADMIN_DSN"), os.Getenv("GO_TEST_AUDIENCE")
	if dsn == "" || admin == "" {
		t.Skip("GO_TEST_PG_DSN / GO_TEST_ADMIN_DSN not set")
	}
	pool, err := pgtx.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	r := &pgtx.Runner{Pool: pool}
	if err := r.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.Features.Daemon {
		t.Skip("the role does not take the daemon road")
	}
	if aud == "" {
		t.Fatal("the daemon road needs GO_TEST_AUDIENCE: the client id the test token is issued for")
	}
	cfg, err := pgx.ParseConfig(admin)
	if err != nil {
		t.Fatal(err)
	}
	mint := func(t *testing.T) pgtx.Session {
		t.Helper()
		conn, err := pgx.Connect(context.Background(), admin)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(context.Background())
		session, token, err := pgtxtest.Login(context.Background(), conn, aud, cfg.User, cfg.Password, "go-platform-test")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if c, err := pgx.Connect(context.Background(), admin); err == nil {
				_, _ = c.Exec(context.Background(), "SELECT api.signout($1)", session)
				c.Close(context.Background())
			}
		})
		return pgtx.Session{Code: session, Token: token, Agent: "go-platform-test", Host: "127.0.0.1"}
	}
	return r, mint
}

// a path under a route the platform guards for any session (GuardSession)
func meReq() *pgtx.Request {
	return &pgtx.Request{Method: "GET", Path: "/api/v2/me", RequestID: "6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d21"}
}

// The whole road: begin, a call, end, commit — the journal line carries the
// status on the wire and the session's user.
func TestIntegrationDaemon_AuthorizedRequestCommitsAndJournals(t *testing.T) {
	r, mint := daemonRoad(t)
	s := mint(t)
	req := meReq()
	var me json.RawMessage
	if err := r.Do(context.Background(), s, req, func(ctx context.Context, tx pgx.Tx) (err error) {
		me, err = pgtx.CallRow(ctx, tx, "current_user", nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(me) == 0 || req.LogID == 0 {
		t.Fatalf("me %s, log %d", me, req.LogID)
	}
	status, errCode := journal(t, r, mint, req.LogID)
	if status != 200 || errCode != "" {
		t.Fatalf("journal: status %d error %q", status, errCode)
	}
}

// A token the database did not issue is a 401 with the catalogue code; the
// handler never runs, and the refusal is journalled by begin itself.
func TestIntegrationDaemon_ForeignTokenIs401(t *testing.T) {
	r, mint := daemonRoad(t)
	s := mint(t)
	s.Token = "eyJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJhY2NvdW50cy50ZXN0IiwiYXVkIjoid2ViLXRlc3QiLCJzdWIiOiJ4In0.c2ln"
	req := meReq()
	err := r.Do(context.Background(), s, req, func(ctx context.Context, tx pgx.Tx) error {
		t.Fatal("handler must not run")
		return nil
	})
	var p *problem.Problem
	if !errors.As(err, &p) || p.Status != 401 || p.Code == nil {
		t.Fatalf("got %v", err)
	}
	// ERR-401-002 wraps whatever the identity step raised — the database's
	// text goes to the log, never to the client
	if *p.Code == "ERR-401-002" && p.Detail != "The access token was not accepted." {
		t.Fatalf("the database's text reached the client: %q", p.Detail)
	}
	t.Logf("401: %s %q / %q", *p.Code, p.Title, p.Detail)
}

// A transaction the handler derives (tx.Begin: a savepoint) stays on the
// daemon road: Call there goes through daemon.call, not a direct call the
// daemon role could not make (and a role with both roads must not).
func TestIntegrationDaemon_NestedTransactionStaysOnTheRoad(t *testing.T) {
	r, mint := daemonRoad(t)
	err := r.Do(context.Background(), mint(t), meReq(), func(ctx context.Context, tx pgx.Tx) error {
		sub, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := pgtx.CallRow(ctx, sub, "current_user", nil); err != nil {
			return err
		}
		return sub.Commit(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// No route in the database for the path: the gateway is closed by default,
// 403 ERR-403-010 before the handler.
func TestIntegrationDaemon_RouteWithoutGuardIs403(t *testing.T) {
	r, mint := daemonRoad(t)
	s := mint(t)
	for _, path := range []string{"/api/v2/no-such-prefix", "/api/v1/whoami", "/api/v2/me/../users"} {
		err := r.Do(context.Background(), s, &pgtx.Request{Method: "GET", Path: path}, func(ctx context.Context, tx pgx.Tx) error {
			t.Fatalf("%s: handler must not run", path)
			return nil
		})
		var p *problem.Problem
		if !errors.As(err, &p) || p.Status != 403 || p.Code == nil || *p.Code != "ERR-403-010" {
			t.Fatalf("%s: got %v", path, err)
		}
	}
}

// A function outside the allow list is refused by daemon.call; the refusal
// is journalled after the rollback to the savepoint, with its code.
func TestIntegrationDaemon_FunctionOutsideTheListIsRefusedAndJournalled(t *testing.T) {
	r, mint := daemonRoad(t)
	s := mint(t)
	req := meReq()
	err := r.Do(context.Background(), s, req, func(ctx context.Context, tx pgx.Tx) error {
		_, err := pgtx.Call(ctx, tx, "su", pgtx.Args{"username": "admin", "password": "x"})
		return err
	})
	var p *problem.Problem
	if !errors.As(err, &p) || p.Code == nil || *p.Code != "ERR-403-012" || p.Status != 403 {
		t.Fatalf("got %v", err)
	}
	if req.LogID == 0 {
		t.Fatal("refusal not journalled")
	}
	if status, code := journal(t, r, mint, req.LogID); status != 403 || code != "ERR-403-012" {
		t.Fatalf("journal: %d %q", status, code)
	}
}

// What the connection sets between calls is not taken as verified: a
// handler that writes current.user / current.access with set_config is
// overwritten by the next daemon.call from the request's row.
func TestIntegrationDaemon_GUCsSetBetweenCallsAreOverwritten(t *testing.T) {
	r, mint := daemonRoad(t)
	s := mint(t)
	var before, after json.RawMessage
	err := r.Do(context.Background(), s, meReq(), func(ctx context.Context, tx pgx.Tx) (err error) {
		if before, err = pgtx.CallRow(ctx, tx, "current_user", nil); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "SELECT set_config('current.user', '00000000-0000-4000-a000-000000000000', true), set_config('current.access', 'false', true)"); err != nil {
			return err
		}
		after, err = pgtx.CallRow(ctx, tx, "current_user", nil)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("identity moved between calls:\n before %s\n after  %s", before, after)
	}
}

// Two sessions under one pool, concurrently: every request is journalled
// under its own session — the line AddApiLog writes in daemon.begin carries
// the session of the context begin opened, not a neighbour's.
func TestIntegrationDaemon_TwoSessionsDoNotLeakIntoEachOther(t *testing.T) {
	r, mint := daemonRoad(t)
	a, b := mint(t), mint(t)
	type done struct {
		code string
		log  int64
		err  error
	}
	var wg sync.WaitGroup
	out := make(chan done, 40)
	for i := 0; i < 20; i++ {
		for _, s := range []pgtx.Session{a, b} {
			wg.Add(1)
			go func(s pgtx.Session) {
				defer wg.Done()
				req := meReq()
				err := r.Do(context.Background(), s, req, func(ctx context.Context, tx pgx.Tx) error {
					time.Sleep(5 * time.Millisecond)
					_, err := pgtx.CallRow(ctx, tx, "current_user", nil)
					return err
				})
				out <- done{s.Code, req.LogID, err}
			}(s)
		}
	}
	wg.Wait()
	close(out)
	var all []done
	for d := range out {
		if d.err != nil || d.log == 0 {
			t.Fatalf("request: %v (log %d)", d.err, d.log)
		}
		all = append(all, d)
	}
	if err := r.Do(context.Background(), mint(t), &pgtx.Request{Method: "GET", Path: "/api/v2/api-log"}, func(ctx context.Context, tx pgx.Tx) error {
		for _, d := range all {
			row, err := pgtx.CallRow(ctx, tx, "get_log", pgtx.Args{"id": d.log})
			if err != nil {
				return err
			}
			var l struct {
				Session string `json:"session"`
			}
			if err := json.Unmarshal(row, &l); err != nil {
				return err
			}
			if l.Session != d.code {
				return errors.New("a request journalled under another session")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// The pooled connection comes back without the request's context.
func TestIntegrationDaemon_ConnectionIsResetAfterRelease(t *testing.T) {
	r, mint := daemonRoad(t)
	if err := r.Do(context.Background(), mint(t), meReq(), func(ctx context.Context, tx pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		var seen *string
		if err := r.Pool.QueryRow(context.Background(), "SELECT NULLIF(current_setting('current.session', true), '')").Scan(&seen); err != nil {
			t.Fatal(err)
		}
		if seen != nil {
			t.Fatalf("connection still carries a session after release")
		}
	}
}

// The catalogue titles of the platform's own refusals are read through
// daemon.error, without a session.
func TestIntegrationDaemon_DetectReadsCatalogueTitles(t *testing.T) {
	r, _ := daemonRoad(t)
	if got := r.Title(problem.CodeLoginFailed, "fallback"); got == "fallback" || got == "" {
		t.Fatalf("title %q", got)
	}
}

// The routes of v2 the database guards.
func TestIntegrationDaemon_Routes(t *testing.T) {
	r, _ := daemonRoad(t)
	routes, err := r.Routes(context.Background(), "v2")
	if err != nil || len(routes) == 0 {
		t.Fatalf("%v %v", routes, err)
	}
	for _, rt := range routes {
		if rt.Path == "/api/v2/me" && rt.Method == "GET" {
			return
		}
	}
	t.Fatalf("no GET /api/v2/me in %v", routes)
}

// journal reads the api_log line under a fresh administrator request.
func journal(t *testing.T, r *pgtx.Runner, mint func(t *testing.T) pgtx.Session, id int64) (status int, code string) {
	t.Helper()
	var row json.RawMessage
	if err := r.Do(context.Background(), mint(t), &pgtx.Request{Method: "GET", Path: "/api/v2/api-log"}, func(ctx context.Context, tx pgx.Tx) (err error) {
		row, err = pgtx.CallRow(ctx, tx, "get_log", pgtx.Args{"id": id})
		return err
	}); err != nil {
		t.Fatalf("read api_log %d: %v", id, err)
	}
	var l struct {
		JSON struct {
			Request struct {
				Status int    `json:"status"`
				Error  string `json:"error"`
			} `json:"_request"`
		} `json:"json"`
	}
	if err := json.Unmarshal(row, &l); err != nil {
		t.Fatalf("api_log %d: %s", id, row)
	}
	return l.JSON.Request.Status, l.JSON.Request.Error
}
