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
	for _, w := range []string{"GET /api/v2/search", "GET /api/v2/objects", "GET /api/v2/objects/{id}", "GET /api/v2/objects/{id}/methods", "POST /api/v2/objects/{id}/actions/{action}", "POST /api/v2/objects/{id}/methods/{method}",
		"GET /api/v2/objects/{id}/access", "PUT /api/v2/objects/{id}/access", "GET /api/v2/objects/{id}/access/decode",
		"GET /api/v2/objects/{id}/files", "POST /api/v2/objects/{id}/files", "DELETE /api/v2/objects/{id}/files", "GET /api/v2/objects/{id}/files/{file}", "DELETE /api/v2/objects/{id}/files/{file}"} {
		method, pattern, _ := strings.Cut(w, " ")
		path := strings.NewReplacer("{id}", id, "{action}", "enable", "{method}", id, "{file}", id).Replace(pattern)
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
		"access: bad id":      {"GET", "/api/v2/objects/nope/access", ""},
		"decode: bad userid":  {"GET", "/api/v2/objects/" + id + "/access/decode?userid=me", ""},
		"grant: no mask":      {"PUT", "/api/v2/objects/" + id + "/access", `{"userid":"` + id + `"}`},
		"grant: no user":      {"PUT", "/api/v2/objects/" + id + "/access", `{"mask":6}`},
		"files: bad filter":   {"GET", "/api/v2/objects/" + id + "/files?filter[name][zz]=1", ""},
		"files: not an array": {"POST", "/api/v2/objects/" + id + "/files", `{"name":"a"}`},
		"files: empty array":  {"POST", "/api/v2/objects/" + id + "/files", `[]`},
		"files: unknown key":  {"POST", "/api/v2/objects/" + id + "/files", `[{"name":"a","owner":"x"}]`},
		"files: file id":      {"POST", "/api/v2/objects/" + id + "/files", `[{"name":"a","file":"` + id + `"}]`},
		"files: null element": {"POST", "/api/v2/objects/" + id + "/files", `[null]`},
		"files: no name":      {"POST", "/api/v2/objects/" + id + "/files", `[{"path":"/x/"}]`},
		"files: size < 0":     {"POST", "/api/v2/objects/" + id + "/files", `[{"name":"a","size":-1}]`},
		"files: size a word":  {"POST", "/api/v2/objects/" + id + "/files", `[{"name":"a","size":"big"}]`},
		"grant: mask 64":      {"PUT", "/api/v2/objects/" + id + "/access", `{"userid":"` + id + `","mask":64}`},
		"grant: class option": {"PUT", "/api/v2/objects/" + id + "/access", `{"userid":"` + id + `","mask":7,"recursive":true}`},
		"file: bad file id":   {"GET", "/api/v2/objects/" + id + "/files/nope", ""},
		"delete: bad file id": {"DELETE", "/api/v2/objects/" + id + "/files/nope", ""},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(c[0], c[1], strings.NewReader(c[2])))
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}
