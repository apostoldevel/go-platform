package log

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/auth/jwt"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/jackc/pgx/v5"
)

type noDB struct{ t *testing.T }

func (n noDB) Do(context.Context, pgtx.Session, *pgtx.Request, func(context.Context, pgx.Tx) error) error {
	n.t.Fatal("database reached")
	return nil
}

var keys = jwt.Keyring{Audiences: map[string]jwt.Key{"web-test": {Alg: "HS256", Secret: []byte("s")}}, Issuers: []string{"accounts.test"}}

func token(sub string) string {
	return jwt.Sign(jwt.Claims{Iss: "accounts.test", Aud: "web-test", Sub: sub, Exp: time.Now().Add(time.Hour).Unix()}, "HS256", []byte("s"))
}

func do(t *testing.T, d Doer, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	h, err := platform.New(platform.Config{Keys: keys}, New(Config{Doer: d}))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token("s"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestModule_NameAndRoutes(t *testing.T) {
	m := New(Config{Doer: noDB{t}})
	if m.Name() != "log" || strings.Join(m.Prefixes(), " ") != "/api/v2/event-log /api/v2/me/event-log" {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
	mux := http.NewServeMux()
	m.Routes(mux)
	for _, w := range []string{"GET /api/v2/event-log", "GET /api/v2/event-log/{id}", "POST /api/v2/event-log", "GET /api/v2/me/event-log", "GET /api/v2/me/event-log/{id}"} {
		method, pattern, _ := strings.Cut(w, " ")
		r, _ := http.NewRequest(method, "http://x"+strings.ReplaceAll(pattern, "{id}", "42"), nil)
		if _, got := mux.Handler(r); got != w {
			t.Fatalf("%s → %q", w, got)
		}
	}
}

func TestEventLog_IDIsAnInteger_WithoutDB(t *testing.T) {
	if rec := do(t, noDB{t}, "GET", "/api/v2/event-log/7f3a0000-0000-4000-8000-000000000001", ""); rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestEventLog_WriteNeedsTypeAndText_WithoutDB(t *testing.T) {
	for _, body := range []string{`{}`, `{"type":"M"}`, `{"type":"X","text":"t"}`, `{"type":"M","text":"t","nope":1}`} {
		if rec := do(t, noDB{t}, "POST", "/api/v2/event-log", body); rec.Code != 400 {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
}
