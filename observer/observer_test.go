package observer

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
	if m.Name() != "observer" || strings.Join(m.Prefixes(), " ") != "/api/v2/observer" {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
	mux := http.NewServeMux()
	m.Routes(mux)
	for _, w := range []string{"GET /api/v2/observer/publishers", "GET /api/v2/observer/publishers/{code}", "GET /api/v2/observer/listeners", "GET /api/v2/observer/listeners/{publisher}/{identity}"} {
		method, pattern, _ := strings.Cut(w, " ")
		path := strings.NewReplacer("{code}", "notify", "{publisher}", "notify", "{identity}", "main").Replace(pattern)
		r, _ := http.NewRequest(method, "http://x"+path, nil)
		if _, got := mux.Handler(r); got != w {
			t.Fatalf("%s → %q", w, got)
		}
	}
	// subscriptions are made over the WebSocket (decision 15): no write verb here
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		r, _ := http.NewRequest(method, "http://x/api/v2/observer/listeners/notify/main", nil)
		if _, got := mux.Handler(r); strings.HasPrefix(got, method+" ") {
			t.Fatalf("%s routed: %q", method, got)
		}
	}
}

func TestCodes_DecidedBeforeTheDatabase(t *testing.T) {
	mux := http.NewServeMux()
	New(Config{Doer: noDB{t}}).Routes(mux)
	for name, path := range map[string]string{
		"publisher code": "/api/v2/observer/publishers/Not%20A%20Code",
		"listener code":  "/api/v2/observer/listeners/x;y/main",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}
