package object

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

const id = "7f3a0000-0000-4000-8000-000000000001"

func TestModule_NameAndRoutes(t *testing.T) {
	m := New(Config{Doer: noDB{t}})
	if m.Name() != "object" || strings.Join(m.Prefixes(), " ") != "/api/v2/objects /api/v2/search" {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
	mux := http.NewServeMux()
	m.Routes(mux)
	for _, w := range []string{"GET /api/v2/search", "GET /api/v2/objects", "GET /api/v2/objects/{id}", "GET /api/v2/objects/{id}/methods", "POST /api/v2/objects/{id}/actions/{action}", "POST /api/v2/objects/{id}/methods/{method}"} {
		method, pattern, _ := strings.Cut(w, " ")
		path := strings.NewReplacer("{id}", id, "{action}", "enable", "{method}", id).Replace(pattern)
		r, _ := http.NewRequest(method, "http://x"+path, nil)
		if _, got := mux.Handler(r); got != w {
			t.Fatalf("%s → %q", w, got)
		}
	}
}

func TestParams_DecidedBeforeTheDatabase(t *testing.T) {
	mux := http.NewServeMux()
	New(Config{Doer: noDB{t}}).Routes(mux)
	for name, c := range map[string][3]string{
		"search: no q":        {"GET", "/api/v2/search", ""},
		"search: bad entity":  {"GET", "/api/v2/search?q=x&entities=Client", ""},
		"action: bad name":    {"POST", "/api/v2/objects/" + id + "/actions/Drop%20It", ""},
		"action: bad body":    {"POST", "/api/v2/objects/" + id + "/actions/enable", "not json"},
		"method: not a uuid":  {"POST", "/api/v2/objects/" + id + "/methods/enable", ""},
		"methods: bad id":     {"GET", "/api/v2/objects/nope/methods", ""},
		"action: body a list": {"POST", "/api/v2/objects/" + id + "/actions/enable", "[1]"},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(c[0], c[1], strings.NewReader(c[2])))
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}
