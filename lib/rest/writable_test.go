package rest_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apostoldevel/go-platform/lib/rest"
)

type xBody struct {
	Code *string `json:"code"`
	Name *string `json:"name"`
}

func (b *xBody) Validate(create bool) error { return rest.Required("code", b.Code, create) }
func (b *xBody) Args(id any) []any          { return []any{id, b.Code, b.Name} }

var xs = rest.Writable{
	Resource: rest.Resource{Prefix: "/api/v2/xs", GetFn: "api.get_x", ListFn: "api.list_x", CountFn: "api.count_x"},
	SetSQL:   "SELECT row_to_json(t) FROM api.set_x($1::uuid, $2, $3) t",
	NewBody:  func() rest.Body { return &xBody{} },
	DeleteFn: "api.delete_x",
}

func TestWritable_RoutesTheFiveVerbs(t *testing.T) {
	mux := http.NewServeMux()
	xs.Routes(mux, noDB{t}, rest.NewIdempotency(0), nil)
	for _, w := range []string{"GET /api/v2/xs", "GET /api/v2/xs/{id}", "POST /api/v2/xs", "PATCH /api/v2/xs/{id}", "DELETE /api/v2/xs/{id}"} {
		method, pattern, _ := strings.Cut(w, " ")
		r, _ := http.NewRequest(method, "http://x"+strings.ReplaceAll(pattern, "{id}", "7f3a0000-0000-4000-8000-000000000001"), nil)
		if _, got := mux.Handler(r); got != w {
			t.Fatalf("%s → %q", w, got)
		}
	}
}

func TestWritable_DecidesBeforeTheDatabase(t *testing.T) {
	mux := http.NewServeMux()
	xs.Routes(mux, noDB{t}, rest.NewIdempotency(0), nil)
	do := func(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	const id = "/api/v2/xs/7f3a0000-0000-4000-8000-000000000001"
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"create without code":   do("POST", "/api/v2/xs", `{"name":"n"}`),
		"create unknown key":    do("POST", "/api/v2/xs", `{"code":"c","nope":1}`),
		"patch bad id":          do("PATCH", "/api/v2/xs/nope", `{}`),
		"patch empty code":      do("PATCH", id, `{"code":""}`, "If-Match", `W/"x"`),
		"delete bad id":         do("DELETE", "/api/v2/xs/nope", ""),
		"create empty body 400": do("POST", "/api/v2/xs", ``),
	} {
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec := do("PATCH", id, `{"name":"n"}`); rec.Code != 428 {
		t.Fatalf("patch without If-Match: %d %s", rec.Code, rec.Body)
	}
}

// Location is derived from the created row's id in the type the resource has.
func TestLocationOf_UUIDAndBigint(t *testing.T) {
	if got := rest.LocationOf(rest.Resource{Prefix: "/api/v2/xs"}, []byte(`{"id":"7f3a0000-0000-4000-8000-000000000001","x":1}`)); got != "/api/v2/xs/7f3a0000-0000-4000-8000-000000000001" {
		t.Fatal(got)
	}
	if got := rest.LocationOf(rest.Resource{Prefix: "/api/v2/js", IntID: true}, []byte(`{"id":7}`)); got != "/api/v2/js/7" {
		t.Fatal(got)
	}
	for _, bad := range []string{`{"id":null}`, `{"x":1}`, `{"id":"nope"}`, `{"id":-1}`} {
		if got := rest.LocationOf(rest.Resource{Prefix: "/api/v2/xs"}, []byte(bad)); got != "" {
			t.Fatalf("%s → %q", bad, got)
		}
	}
}

type twoBody struct {
	A *string `json:"a"`
	B *string `json:"b"`
}

func (b *twoBody) Validate(create bool) error {
	return rest.RequiredAll(create, rest.Req("a", b.A), rest.Req("b", b.B))
}
func (b *twoBody) Args(id any) []any { return []any{id, b.A, b.B} }

func TestRequiredAll_NamesTheFirstMissing(t *testing.T) {
	a := "x"
	if err := (&twoBody{A: &a}).Validate(true); err == nil || !strings.Contains(err.Error(), "b is required") {
		t.Fatalf("%v", err)
	}
	if err := (&twoBody{}).Validate(false); err != nil {
		t.Fatalf("absent on PATCH is fine: %v", err)
	}
}
