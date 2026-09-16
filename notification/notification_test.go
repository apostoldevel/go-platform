package notification

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
	if m.Name() != "notification" || strings.Join(m.Prefixes(), " ") != "/api/v2/notifications" {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
	mux := http.NewServeMux()
	m.Routes(mux)
	for _, w := range []string{"GET /api/v2/notifications", "GET /api/v2/notifications/{id}", "GET /api/v2/notifications/since", "GET /api/v2/notifications/changed"} {
		method, pattern, _ := strings.Cut(w, " ")
		r, _ := http.NewRequest(method, "http://x"+strings.ReplaceAll(pattern, "{id}", "7f3a0000-0000-4000-8000-000000000001"), nil)
		if _, got := mux.Handler(r); got != w {
			t.Fatalf("%s → %q", w, got)
		}
	}
}

func TestParams_DecidedBeforeTheDatabase(t *testing.T) {
	mux := http.NewServeMux()
	New(Config{Doer: noDB{t}}).Routes(mux)
	for name, path := range map[string]string{
		"since: bad from":       "/api/v2/notifications/since?from=yesterday",
		"changed: no objects":   "/api/v2/notifications/changed",
		"changed: bad object":   "/api/v2/notifications/changed?objects=nope",
		"changed: bad from":     "/api/v2/notifications/changed?objects=7f3a0000-0000-4000-8000-000000000001&from=x",
		"changed: many objects": "/api/v2/notifications/changed?objects=" + strings.Repeat("7f3a0000-0000-4000-8000-000000000001,", maxObjects+1),
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

// The entity code names api.get_<code>: only a plain identifier may reach the SQL.
func TestGetFnOf_PlainIdentifierOnly(t *testing.T) {
	if fn, ok := getFnOf("charge_point"); !ok || fn != "api.get_charge_point" {
		t.Fatal(fn, ok)
	}
	for _, bad := range []string{"", "x;drop", "Client", "a-b", "1x"} {
		if _, ok := getFnOf(bad); ok {
			t.Fatalf("%q accepted", bad)
		}
	}
}
