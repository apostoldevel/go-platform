//go:build integration

package log

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/jackc/pgx/v5"
)

func live(t *testing.T) (http.Handler, *pgtx.Runner, pgtx.Session) {
	t.Helper()
	dsn, admin := os.Getenv("GO_TEST_PG_DSN"), os.Getenv("GO_TEST_ADMIN_DSN")
	if dsn == "" || admin == "" {
		t.Skip("GO_TEST_PG_DSN / GO_TEST_ADMIN_DSN not set")
	}
	pool, err := pgtx.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	cfg, _ := pgx.ParseConfig(admin)
	conn, err := pgx.Connect(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	var session string
	if err := conn.QueryRow(context.Background(), "SELECT session FROM api.login($1, $2, 'go-log-test', '127.0.0.1')", cfg.User, cfg.Password).Scan(&session); err != nil {
		t.Fatal(err)
	}
	conn.Close(context.Background())
	t.Cleanup(func() {
		if c, err := pgx.Connect(context.Background(), admin); err == nil {
			_, _ = c.Exec(context.Background(), "SELECT api.signout($1)", session)
			c.Close(context.Background())
		}
	})
	runner := &pgtx.Runner{Pool: pool, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))}
	if err := runner.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	h, err := platform.New(platform.Config{Keys: keys}, New(Config{Doer: runner}))
	if err != nil {
		t.Fatal(err)
	}
	return h, runner, pgtx.Session{Code: session, Agent: "go-log-test", Host: "127.0.0.1"}
}

func call(h http.Handler, sess pgtx.Session, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token(sess.Code))
	req.Header.Set("X-Request-Id", "it-log-"+method)
	req.Header.Set("User-Agent", sess.Agent)
	req.Header.Set("X-Forwarded-For", sess.Host)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestIntegration_EventLogReadParity(t *testing.T) {
	h, runner, sess := live(t)

	// list, newest first; one row for parity with api.get_event_log
	rec := call(h, sess, "GET", "/api/v2/event-log?sort=-datetime&page[limit]=5", "")
	var list struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != 200 || list.Total < 1 || len(list.Items) == 0 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	id := strconv.FormatInt(list.Items[0].ID, 10)
	rec = call(h, sess, "GET", "/api/v2/event-log/"+id, "")
	var want json.RawMessage
	if err := runner.Do(context.Background(), sess, &pgtx.Request{Method: "TEST", Path: "/parity"}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT row_to_json(t) FROM api.get_event_log($1::bigint) t", list.Items[0].ID).Scan(&want)
	}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || !sameJSON(rec.Body.Bytes(), want) {
		t.Fatalf("get/parity: %d\n v2 %s\n v1 %s", rec.Code, rec.Body, want)
	}
	if rec = call(h, sess, "GET", "/api/v2/event-log/999999999999", ""); rec.Code != 404 {
		t.Fatalf("missing row: %d %s", rec.Code, rec.Body)
	}
	// the user's journal: one row is fast; the list is not exercised here —
	// api.count_user_log(NULL) takes ~42 s on the dev database (api.user_log
	// joins EventLog by username through a CTE — a database change), and v1's
	// /event/log/count pays the same
	if rec = call(h, sess, "GET", "/api/v2/me/event-log/"+id, ""); rec.Code != 200 && rec.Code != 404 {
		t.Fatalf("me/event-log row: %d %s", rec.Code, rec.Body)
	}
}

// api.write_to_log of db-platform 1.2.20 calls AddEventLog with an ambiguous
// argument list (SQLSTATE 42725) — v1 /admin/event/log/set fails the same
// way; the v2 route is right, the function is not.
func TestIntegration_EventLogWrite(t *testing.T) {
	h, runner, sess := live(t)
	probe := runner.Do(context.Background(), sess, &pgtx.Request{Method: "TEST", Path: "/probe"}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "SELECT api.write_to_log('M', 1000, 'go-test', 'probe')")
		return err
	})
	rec := call(h, sess, "POST", "/api/v2/event-log", `{"type":"M","code":1000,"scope":"go-test","text":"written by the v2 integration test"}`)
	if probe != nil {
		if rec.Code != 500 {
			t.Fatalf("write with a broken api.write_to_log: %d %s", rec.Code, rec.Body)
		}
		t.Logf("api.write_to_log is broken in this db-platform (%v) — POST /api/v2/event-log answers 500 until the database fixes it", probe)
		return
	}
	if rec.Code != 201 || rec.Header().Get("Location") == "" {
		t.Fatalf("write: %d %s", rec.Code, rec.Body)
	}
	if rec = call(h, sess, "GET", rec.Header().Get("Location"), ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "written by the v2") {
		t.Fatalf("written row: %d %s", rec.Code, rec.Body)
	}
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return string(xa) == string(ya)
}
