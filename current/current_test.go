package current

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
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
		"interface code": `{"interface":"default"}`,
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("PATCH", "/api/v2/me", strings.NewReader(body)))
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

// area/interface/locale take a uuid or a code: the function and its key are
// chosen from the shape of the value — by the parameter's name for locale
// (pLocale / pCode), by a function of its own for an area's code
// (set_session_area_by_code: daemon.call chooses a form by keys, and both
// set_session_area forms have one key); interface is a uuid.
func TestSessionCall_UUIDOrCode(t *testing.T) {
	const id = "7f3a0000-0000-4000-8000-000000000001"
	for _, c := range []struct {
		name, v string
		fn      string
		want    pgtx.Args
	}{
		{"locale", id, "set_session_locale", pgtx.Args{"locale": id}},
		{"locale", "ru", "set_session_locale", pgtx.Args{"code": "ru"}},
		{"area", id, "set_session_area", pgtx.Args{"area": id}},
		{"area", "default", "set_session_area_by_code", pgtx.Args{"code": "default"}},
		{"interface", id, "set_session_interface", pgtx.Args{"interface": id}},
	} {
		if fn, got := sessionCall(c.name, c.v); fn != c.fn || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %q: %s %#v, want %s %#v", c.name, c.v, fn, got, c.fn, c.want)
		}
	}
}
