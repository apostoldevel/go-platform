package verification

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
	if m.Name() != "verification" || strings.Join(m.Prefixes(), " ") != "/api/v2/verification" {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
	mux := http.NewServeMux()
	m.Routes(mux)
	for _, w := range []string{"GET /api/v2/verification/codes", "GET /api/v2/verification/codes/{id}", "POST /api/v2/verification/codes", "POST /api/v2/verification/codes/confirm"} {
		method, pattern, _ := strings.Cut(w, " ")
		r, _ := http.NewRequest(method, "http://x"+strings.ReplaceAll(pattern, "{id}", "7f3a0000-0000-4000-8000-000000000001"), nil)
		if _, got := mux.Handler(r); got != w {
			t.Fatalf("%s → %q", w, got)
		}
	}
}

func TestBodies_DecidedBeforeTheDatabase(t *testing.T) {
	mux := http.NewServeMux()
	New(Config{Doer: noDB{t}}).Routes(mux)
	for name, c := range map[string][2]string{
		"new: no type":         {"/api/v2/verification/codes", `{}`},
		"new: bad type":        {"/api/v2/verification/codes", `{"type":"fax"}`},
		"new: empty code":      {"/api/v2/verification/codes", `{"type":"email","code":""}`},
		"confirm: no code":     {"/api/v2/verification/codes/confirm", `{"type":"email"}`},
		"confirm: bad type":    {"/api/v2/verification/codes/confirm", `{"type":"M","code":"x"}`},
		"confirm: unknown key": {"/api/v2/verification/codes/confirm", `{"type":"email","code":"x","user":"u"}`},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", c[0], strings.NewReader(c[1])))
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

func TestKindOf_EmailIsM_PhoneIsP(t *testing.T) {
	if k, ok := kindOf("email"); !ok || k != "M" {
		t.Fatal(k, ok)
	}
	if k, ok := kindOf("phone"); !ok || k != "P" {
		t.Fatal(k, ok)
	}
	if _, ok := kindOf("M"); ok {
		t.Fatal("the database letter is not the wire word")
	}
}
