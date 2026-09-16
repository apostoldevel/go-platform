package registry

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
	if m.Name() != "registry" || strings.Join(m.Prefixes(), " ") != "/api/v2/registry" {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
	mux := http.NewServeMux()
	m.Routes(mux)
	for _, w := range []string{
		"GET /api/v2/registry", "GET /api/v2/registry/keys", "GET /api/v2/registry/keys/{id}/path", "GET /api/v2/registry/keys/enum",
		"GET /api/v2/registry/values", "GET /api/v2/registry/values/read", "PUT /api/v2/registry/values",
		"DELETE /api/v2/registry/values/{id}", "DELETE /api/v2/registry/values", "DELETE /api/v2/registry/keys", "DELETE /api/v2/registry/tree",
	} {
		method, pattern, _ := strings.Cut(w, " ")
		r, _ := http.NewRequest(method, "http://x"+strings.ReplaceAll(pattern, "{id}", "7f3a0000-0000-4000-8000-000000000001"), nil)
		if _, got := mux.Handler(r); got != w {
			t.Fatalf("%s → %q", w, got)
		}
	}
}

func TestValidation_WithoutDB(t *testing.T) {
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"write without name":      do(t, noDB{t}, "PUT", "/api/v2/registry/values", `{"key":"CURRENT_USER","subkey":"x","type":"string","value":"v"}`),
		"write without key or id": do(t, noDB{t}, "PUT", "/api/v2/registry/values", `{"name":"n","type":"string","value":"v"}`),
		"write bad type":          do(t, noDB{t}, "PUT", "/api/v2/registry/values", `{"key":"CURRENT_USER","subkey":"x","name":"n","type":"blob","value":"v"}`),
		"write integer as text":   do(t, noDB{t}, "PUT", "/api/v2/registry/values", `{"key":"CURRENT_USER","subkey":"x","name":"n","type":"integer","value":"v"}`),
		"write unknown key":       do(t, noDB{t}, "PUT", "/api/v2/registry/values", `{"key":"CURRENT_USER","subkey":"x","name":"n","type":"string","value":"v","nope":1}`),
		"read without name":       do(t, noDB{t}, "GET", "/api/v2/registry/values/read?key=CURRENT_USER&subkey=x", ""),
		"delete value bad id":     do(t, noDB{t}, "DELETE", "/api/v2/registry/values/nope", ""),
		"delete value nothing":    do(t, noDB{t}, "DELETE", "/api/v2/registry/values", ""),
		"delete tree without key": do(t, noDB{t}, "DELETE", "/api/v2/registry/tree?subkey=x", ""),
		"bad uuid in query":       do(t, noDB{t}, "GET", "/api/v2/registry/keys?parent=nope", ""),
	} {
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}
