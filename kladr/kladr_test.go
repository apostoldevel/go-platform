package kladr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	if m.Name() != "kladr" || strings.Join(m.Prefixes(), " ") != "/api/v2/kladr" {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
	mux := http.NewServeMux()
	m.Routes(mux)
	for _, w := range []string{"GET /api/v2/kladr", "GET /api/v2/kladr/{id}", "GET /api/v2/kladr/{id}/history", "GET /api/v2/kladr/string"} {
		method, pattern, _ := strings.Cut(w, " ")
		r, _ := http.NewRequest(method, "http://x"+strings.ReplaceAll(pattern, "{id}", "42"), nil)
		if _, got := mux.Handler(r); got != w {
			t.Fatalf("%s → %q", w, got)
		}
	}
}

func TestParams_DecidedBeforeTheDatabase(t *testing.T) {
	mux := http.NewServeMux()
	New(Config{Doer: noDB{t}}).Routes(mux)
	for name, path := range map[string]string{
		"get: not an integer":     "/api/v2/kladr/abc",
		"get: out of range":       "/api/v2/kladr/4294967296",
		"history: not an integer": "/api/v2/kladr/-1/history",
		"string: no code":         "/api/v2/kladr/string",
		"string: bad code":        "/api/v2/kladr/string?code=12%2034",
		"string: bad short":       "/api/v2/kladr/string?code=7700000000000&short=x",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}
