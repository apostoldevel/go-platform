//go:build integration

package api

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
	if err := conn.QueryRow(context.Background(), "SELECT session FROM api.login($1, $2, 'go-api-test', '127.0.0.1')", cfg.User, cfg.Password).Scan(&session); err != nil {
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
	return h, runner, pgtx.Session{Code: session, Agent: "go-api-test", Host: "127.0.0.1"}
}

func call(h http.Handler, sess pgtx.Session, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token(sess.Code))
	req.Header.Set("X-Request-Id", "it-api-"+method)
	req.Header.Set("User-Agent", sess.Agent)
	req.Header.Set("X-Forwarded-For", sess.Host)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestIntegration_APILogReadParity(t *testing.T) {
	h, runner, sess := live(t)
	rec := call(h, sess, "GET", "/api/v2/api-log?sort=-datetime&page[limit]=5", "")
	var list struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != 200 || list.Total < 1 || len(list.Items) == 0 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	rec = call(h, sess, "GET", "/api/v2/api-log/"+strconv.FormatInt(list.Items[0].ID, 10), "")
	var want json.RawMessage
	if err := runner.Do(context.Background(), sess, &pgtx.Request{Method: "TEST", Path: "/parity"}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT row_to_json(t) FROM api.get_log($1::bigint) t", list.Items[0].ID).Scan(&want)
	}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || !sameJSON(rec.Body.Bytes(), want) {
		t.Fatalf("get/parity: %d\n v2 %s\n v1 %s", rec.Code, rec.Body, want)
	}
	if rec = call(h, sess, "GET", "/api/v2/api-log/999999999999", ""); rec.Code != 404 {
		t.Fatalf("missing row: %d %s", rec.Code, rec.Body)
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
