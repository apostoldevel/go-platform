// Package resttest is the shared scaffolding of the packages' integration
// tests: a pool as the module's role, an administrator's session minted with
// the admin DSN, a host around the package under test, calls with the
// headers the gateway forwards, and api.* run directly through the same
// transaction for parity with the v1 answer.
package resttest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/auth/jwt"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/jackc/pgx/v5"
)

// Keys is the test keyring; Token signs a session code with it.
var Keys = jwt.Keyring{Audiences: map[string]jwt.Key{"web-test": {Alg: "HS256", Secret: []byte("s")}}, Issuers: []string{"accounts.test"}}

// Token is a bearer token of the test audience for the session code.
func Token(sub string) string {
	return jwt.Sign(jwt.Claims{Iss: "accounts.test", Aud: "web-test", Sub: sub, Exp: time.Now().Add(time.Hour).Unix()}, "HS256", []byte("s"))
}

// Live is one integration-test environment: GO_TEST_PG_DSN (the pool, the
// module's role), GO_TEST_ADMIN_DSN (mints the administrator's session).
type Live struct {
	Handler http.Handler
	Runner  *pgtx.Runner
	Session pgtx.Session
	agent   string
}

// Start skips the test without the environment; otherwise it mints a session
// (signed out at the end), detects the database's features and hosts the
// module New builds over the runner.
func Start(t *testing.T, agent string, mod func(runner *pgtx.Runner) platform.Module) *Live {
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
	if err := conn.QueryRow(context.Background(), "SELECT session FROM api.login($1, $2, $3, '127.0.0.1')", cfg.User, cfg.Password, agent).Scan(&session); err != nil {
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
	h, err := platform.New(platform.Config{Keys: Keys, Catalogue: runner}, mod(runner))
	if err != nil {
		t.Fatal(err)
	}
	return &Live{Handler: h, Runner: runner, Session: pgtx.Session{Code: session, Agent: agent, Host: "127.0.0.1"}, agent: agent}
}

// Call sends one request as the gateway would: bearer of the session, request
// id, agent and X-Forwarded-For; extra headers in pairs.
func (l *Live) Call(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+Token(l.Session.Code))
	req.Header.Set("X-Request-Id", "it-"+l.agent+"-"+method)
	req.Header.Set("User-Agent", l.Session.Agent)
	req.Header.Set("X-Forwarded-For", l.Session.Host)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	l.Handler.ServeHTTP(rec, req)
	return rec
}

// Direct runs one api.* query through the same request transaction the
// module uses — the v1 answer without the v1 envelope.
func (l *Live) Direct(t *testing.T, sql string, args ...any) json.RawMessage {
	t.Helper()
	var row json.RawMessage
	if err := l.Runner.Do(context.Background(), l.Session, &pgtx.Request{Method: "TEST", Path: "/parity"}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, sql, args...).Scan(&row)
	}); err != nil {
		t.Fatal(err)
	}
	return row
}

// Page is the shape of a list answer.
type Page struct {
	Items []map[string]any `json:"items"`
	Total int64            `json:"total"`
}

// ListOf fails unless rec is a 200 list with at least one item.
func ListOf(t *testing.T, rec *httptest.ResponseRecorder, what string) Page {
	t.Helper()
	var p Page
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || rec.Code != 200 || p.Total < 1 || len(p.Items) == 0 {
		t.Fatalf("%s: %d %s", what, rec.Code, rec.Body)
	}
	return p
}

// SameJSON compares two JSON documents structurally.
func SameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return string(xa) == string(ya)
}
