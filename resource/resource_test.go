package resource

import (
	"context"
	"net/http"
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
	if m.Name() != "resource" || strings.Join(m.Prefixes(), " ") != "/api/v2/resources" {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
	mux := http.NewServeMux()
	m.Routes(mux)
	for _, w := range []string{"GET /api/v2/resources", "GET /api/v2/resources/{id}", "POST /api/v2/resources", "PATCH /api/v2/resources/{id}", "DELETE /api/v2/resources/{id}"} {
		method, pattern, _ := strings.Cut(w, " ")
		r, _ := http.NewRequest(method, "http://x"+strings.ReplaceAll(pattern, "{id}", "7f3a0000-0000-4000-8000-000000000001"), nil)
		if _, got := mux.Handler(r); got != w {
			t.Fatalf("%s → %q", w, got)
		}
	}
}
