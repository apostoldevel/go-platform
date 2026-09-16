package error

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
	if m.Name() != "error" || strings.Join(m.Prefixes(), " ") != "/api/v2/errors" {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
	mux := http.NewServeMux()
	m.Routes(mux)
	for _, w := range []string{"GET /api/v2/errors", "GET /api/v2/errors/{id}", "POST /api/v2/errors", "PATCH /api/v2/errors/{id}", "GET /api/v2/errors/by-code/{code}"} {
		method, pattern, _ := strings.Cut(w, " ")
		path := strings.ReplaceAll(strings.ReplaceAll(pattern, "{id}", "7f3a0000-0000-4000-8000-000000000001"), "{code}", "ERR-404-001")
		r, _ := http.NewRequest(method, "http://x"+path, nil)
		if _, got := mux.Handler(r); got != w {
			t.Fatalf("%s → %q", w, got)
		}
	}
	// the catalogue has no api.delete_error: DELETE is not a route
	r, _ := http.NewRequest("DELETE", "http://x/api/v2/errors/7f3a0000-0000-4000-8000-000000000001", nil)
	if _, got := mux.Handler(r); strings.HasPrefix(got, "DELETE") {
		t.Fatalf("DELETE routed: %q", got)
	}
}

func TestCreate_RequiresCodeAndHTTPCode_BeforeTheDatabase(t *testing.T) {
	mux := http.NewServeMux()
	New(Config{Doer: noDB{t}}).Routes(mux)
	for name, body := range map[string]string{
		"no code":      `{"http_code":400}`,
		"no http_code": `{"code":"ERR-999-001"}`,
		"bad code":     `{"code":"E1","http_code":400}`,
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v2/errors", strings.NewReader(body)))
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

func TestDefaults_SeverityAndCategoryAsAddError(t *testing.T) {
	b := &body{}
	errors.Defaults(b)
	if b.Severity == nil || *b.Severity != "E" || b.Category == nil || *b.Category != "validation" {
		t.Fatalf("%+v", b)
	}
	given := &body{Severity: ptr("W"), Category: ptr("access")}
	errors.Defaults(given)
	if *given.Severity != "W" || *given.Category != "access" {
		t.Fatalf("%+v", given)
	}
}

func TestByCode_RefusesAnythingButACatalogueCode(t *testing.T) {
	mux := http.NewServeMux()
	New(Config{Doer: noDB{t}}).Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v2/errors/by-code/not-a-code", nil))
	if rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
