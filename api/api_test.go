package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/apostoldevel/go-platform/lib/auth/jwt"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/jackc/pgx/v5"
)

type noDB struct{ t *testing.T }

func (n noDB) Do(context.Context, pgtx.Session, *pgtx.Request, func(context.Context, pgx.Tx) error) error {
	n.t.Fatal("database reached")
	return nil
}

func TestModule_NameAndRoutes(t *testing.T) {
	m := New(Config{Doer: noDB{t}})
	if m.Name() != "api" || strings.Join(m.Prefixes(), " ") != "/api/v2/api-log" {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
	mux := http.NewServeMux()
	m.Routes(mux)
	for _, w := range []string{"GET /api/v2/api-log", "GET /api/v2/api-log/{id}"} {
		method, pattern, _ := strings.Cut(w, " ")
		r, _ := http.NewRequest(method, "http://x"+strings.ReplaceAll(pattern, "{id}", "42"), nil)
		if _, got := mux.Handler(r); got != w {
			t.Fatalf("%s → %q", w, got)
		}
	}
}

var keys = jwt.Keyring{Audiences: map[string]jwt.Key{"web-test": {Alg: "HS256", Secret: []byte("s")}}, Issuers: []string{"accounts.test"}}

func token(sub string) string {
	return jwt.Sign(jwt.Claims{Iss: "accounts.test", Aud: "web-test", Sub: sub, Exp: time.Now().Add(time.Hour).Unix()}, "HS256", []byte("s"))
}
