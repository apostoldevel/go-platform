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

// area/interface/locale take a uuid or a code: the overload is chosen from
// the shape of the value — by the parameter's name for locale (pLocale /
// pCode), by a pinned type for area (one name for both); interface is a uuid.
func TestSessionArgs_UUIDOrCode(t *testing.T) {
	const id = "7f3a0000-0000-4000-8000-000000000001"
	for _, c := range []struct {
		name, v string
		want    pgtx.Args
	}{
		{"locale", id, pgtx.Args{"locale": id}},
		{"locale", "ru", pgtx.Args{"code": "ru"}},
		{"area", id, pgtx.Args{"area": pgtx.Typed{V: id, Type: "uuid"}}},
		{"area", "default", pgtx.Args{"area": pgtx.Typed{V: "default", Type: "text"}}},
		{"interface", id, pgtx.Args{"interface": id}},
	} {
		if got := sessionArgs(c.name, c.v); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %q: %#v, want %#v", c.name, c.v, got, c.want)
		}
	}
}
