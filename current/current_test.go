package current

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
	if m.Name() != "current" || strings.Join(m.Prefixes(), " ") != "/api/v2/me" {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
	mux := http.NewServeMux()
	m.Routes(mux)
	for _, w := range []string{"GET /api/v2/me", "PATCH /api/v2/me"} {
		method, pattern, _ := strings.Cut(w, " ")
		r, _ := http.NewRequest(method, "http://x"+pattern, nil)
		if _, got := mux.Handler(r); got != w {
			t.Fatalf("%s → %q", w, got)
		}
	}
}

func TestPatch_DecidesBeforeTheDatabase(t *testing.T) {
	mux := http.NewServeMux()
	New(Config{Doer: noDB{t}}).Routes(mux)
	for name, body := range map[string]string{
		"empty body":     ``,
		"nothing to set": `{}`,
		"unknown key":    `{"session":"x"}`,
		"empty area":     `{"area":""}`,
		"bad oper_date":  `{"oper_date":"yesterday"}`,
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("PATCH", "/api/v2/me", strings.NewReader(body)))
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

// area/interface/locale take a uuid or a code: the cast of the api.* overload
// is chosen from the shape of the value.
func TestCastOf_UUIDOrCode(t *testing.T) {
	if got := castOf("7f3a0000-0000-4000-8000-000000000001"); got != "::uuid" {
		t.Fatal(got)
	}
	if got := castOf("ru"); got != "::text" {
		t.Fatal(got)
	}
}
