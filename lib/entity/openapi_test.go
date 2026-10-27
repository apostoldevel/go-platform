package entity

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/apostoldevel/go-platform/internal/entitytest"
	"github.com/apostoldevel/go-platform/lib/openapi"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/query"
	"github.com/apostoldevel/go-platform/lib/rest"
)

type thingBody struct {
	Code *string `json:"code" required:"create" doc:"Code."`
	Name *string `json:"name" doc:"Name."`
}

func (b *thingBody) Validate(create bool) error {
	return rest.RequiredAll(create, rest.Req("code", b.Code))
}
func (b *thingBody) Args(id any) pgtx.Args {
	return Named(b, []any{id, strArg(b.Code), strArg(b.Name)})
}

// strArg is the argument of a text key.
func strArg(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

type thingRow struct {
	ID   string  `json:"id" format:"uuid" doc:"Id."`
	Code string  `json:"code" doc:"Code."`
	Name *string `json:"name" doc:"Name."`
}

func thing(t *testing.T, row any) *Module {
	t.Helper()
	return New(Entity{
		Name: "thing_kind", Prefix: "/api/v2/thing-kinds",
		Resource: rest.Resource{Prefix: "/api/v2/thing-kinds", GetFn: "api.get_thing", ListFn: "api.list_thing", CountFn: "api.count_thing"},
		SetFn:    "api.set_thing", NewBody: func() rest.Body { return &thingBody{} },
		SearchFields: []string{"name", "code"}, Row: row,
	}, Config{Doer: entitytest.NoDB{T: t}})
}

// An entity that declares no Row is not described; one that does is, by all
// six routes of the form, and what is described is what is mounted.
func TestOperations_SixRoutesAreTheMountedOnes(t *testing.T) {
	if ops := thing(t, nil).Operations(); ops != nil {
		t.Fatalf("an entity without a Row is described: %v", ops)
	}
	m := thing(t, thingRow{})
	ops := m.Operations()
	var got []string
	for _, o := range ops {
		got = append(got, o.Pattern())
	}
	want := []string{
		"GET /api/v2/thing-kinds", "GET /api/v2/thing-kinds/{id}", "POST /api/v2/thing-kinds", "PATCH /api/v2/thing-kinds/{id}",
		"POST /api/v2/thing-kinds/{id}/actions/{action}", "GET /api/v2/thing-kinds/{id}/methods",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%v", got)
	}
	mux := http.NewServeMux()
	m.Routes(mux)
	if missing, undocumented := openapi.Gaps(mux, ops); len(missing)+len(undocumented) != 0 {
		t.Fatalf("missing %v, undocumented %v", missing, undocumented)
	}
	if _, err := openapi.Build(openapi.Info{Title: "t", Version: "1"}, ops); err != nil {
		t.Fatal(err)
	}
	// operation ids and names come from the entity, not from a template shared by all
	if ops[0].ID != "thing_kinds_list" || ops[0].Tag != "thing-kinds" || ops[0].Summary != "List thing kinds" {
		t.Fatalf("%+v", ops[0])
	}
}

// the mutant of the project's module: a route beside the six that the
// description does not know — the gap must be seen
func TestOperations_ARouteOfThePackagesOwnIsSeen(t *testing.T) {
	m := thing(t, thingRow{})
	mux := http.NewServeMux()
	m.Routes(mux)
	mux.HandleFunc("DELETE /api/v2/thing-kinds/{id}", func(http.ResponseWriter, *http.Request) {})
	_, undocumented := openapi.Gaps(mux, m.Operations())
	if !slices.Equal(undocumented, []string{"DELETE /api/v2/thing-kinds/{id}"}) {
		t.Fatalf("%v", undocumented)
	}
}

// What the description says the handler answers is what it answers: every
// refusal made before the database is among the statuses of its operation,
// with the media type the description gives.
func TestOperations_StatusesOfRefusalsAreDeclared(t *testing.T) {
	m := thing(t, thingRow{})
	h := entitytest.Srv(t, m)
	declared := map[string][]string{}
	for _, o := range m.Operations() {
		for _, r := range o.Responses {
			declared[o.Pattern()] = append(declared[o.Pattern()], r.Status)
		}
	}
	p, id := "/api/v2/thing-kinds", entitytest.ID
	for _, c := range []struct {
		pattern, path, body string
		hdr                 []string
		status              string
	}{
		{"GET " + p, p + "?page[limit]=0", "", nil, "400"},
		{"GET " + p, p + "?limit=10", "", nil, "400"},
		{"GET " + p + "/{id}", p + "/not-a-uuid", "", nil, "400"},
		{"POST " + p, p, "", nil, "400"},
		{"POST " + p, p, `{"name":"x"}`, nil, "400"},
		{"POST " + p, p, `{"code":"c","nope":1}`, nil, "400"},
		{"PATCH " + p + "/{id}", p + "/" + id, `{"name":"x"}`, nil, "428"},
		{"PATCH " + p + "/{id}", p + "/" + id, `{}`, []string{"If-Match", `W/"1"`}, "400"},
		{"POST " + p + "/{id}/actions/{action}", p + "/" + id + "/actions/Enable", "", nil, "400"},
		{"POST " + p + "/{id}/actions/{action}", p + "/" + id + "/actions/enable", strings.Repeat(" ", 1<<20+1) + "{}", nil, "413"},
		{"GET " + p + "/{id}/methods", p + "/" + id + "/methods?sort=sequence", "", nil, "400"},
	} {
		method, _, _ := strings.Cut(c.pattern, " ")
		rec := entitytest.Do(h, method, c.path, c.body, c.hdr...)
		if !slices.Contains(declared[c.pattern], c.status) || strconv.Itoa(rec.Code) != c.status {
			t.Errorf("%s %s: answered %d, expected %s, declared %v", method, c.path, rec.Code, c.status, declared[c.pattern])
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("%s %s: content type %q", method, c.path, ct)
		}
	}
}

// The statuses and headers the description gives to the success of each
// route are the code's: 201 with Location on create, 304 and 204 where the
// handler makes them. The handlers are read here, not the database's answer.
func TestOperations_SuccessShapesAreStated(t *testing.T) {
	by := map[string]map[string]openapi.Response{}
	for _, o := range thing(t, thingRow{}).Operations() {
		by[o.Pattern()] = map[string]openapi.Response{}
		for _, r := range o.Responses {
			by[o.Pattern()][r.Status] = r
		}
	}
	p := "/api/v2/thing-kinds"
	for pattern, statuses := range map[string][]string{
		"GET " + p:                             {"200"},
		"GET " + p + "/{id}":                   {"200", "304"},
		"POST " + p:                            {"201", "409"},
		"PATCH " + p + "/{id}":                 {"200", "412", "428"},
		"POST " + p + "/{id}/actions/{action}": {"200", "204", "413"},
		"GET " + p + "/{id}/methods":           {"200"},
	} {
		for _, s := range statuses {
			if _, ok := by[pattern][s]; !ok {
				t.Errorf("%s: %s is not described", pattern, s)
			}
		}
		for _, s := range []string{"401", "403", "default"} {
			if r := by[pattern][s]; !r.Problem {
				t.Errorf("%s: %s must be problem+json", pattern, s)
			}
		}
	}
	// If-Match is required exactly where the handler answers 428 without it
	// (TestOperations_StatusesOfRefusalsAreDeclared makes the 428 happen)
	for _, o := range thing(t, thingRow{}).Operations() {
		for _, pa := range o.Params {
			if pa.In == "header" && pa.Name == "If-Match" && !pa.Required {
				t.Errorf("%s: If-Match is not marked required", o.Pattern())
			}
		}
	}
	has := func(r openapi.Response, h string) bool {
		for _, x := range r.Headers {
			if x.Name == h {
				return true
			}
		}
		return false
	}
	if r := by["POST "+p]["201"]; !has(r, "Location") || !has(r, "ETag") {
		t.Errorf("201: %v", r.Headers)
	}
	if !has(by["GET "+p+"/{id}"]["200"], "ETag") || !has(by["PATCH "+p+"/{id}"]["200"], "ETag") {
		t.Error("ETag of a row is not described")
	}
}

// the operators the description lists are the ones lib/query takes, and one
// it does not list is refused — the list cannot drift from the parser
func TestOperations_ListedOperatorsAreTheParsers(t *testing.T) {
	var listed string
	for _, o := range thing(t, thingRow{}).Operations() {
		if o.Pattern() == "GET /api/v2/thing-kinds" {
			for _, pa := range o.Params {
				if pa.Name == "filter" {
					listed = pa.Description
				}
			}
		}
	}
	for _, op := range []string{"eq", "ne", "lt", "lte", "gt", "gte", "like", "ilike", "in", "null"} {
		if !strings.Contains(listed, " "+op) && !strings.Contains(listed, ", "+op) {
			t.Errorf("operator %s is not listed", op)
		}
		v := "1"
		if op == "null" {
			v = "true"
		}
		if _, err := query.Parse(url.Values{"filter[name][" + op + "]": {v}}); err != nil {
			t.Errorf("operator %s listed and refused: %v", op, err)
		}
	}
	if _, err := query.Parse(url.Values{"filter[name][between]": {"1"}}); err == nil || strings.Contains(listed, "between") {
		t.Error("an operator the parser refuses is listed")
	}
}

// the page bounds the description gives are the parser's: the largest page
// is accepted, one more is refused, the default is what an unpaged list gets
func TestOperations_PageBoundsAreTheParsers(t *testing.T) {
	var limit map[string]any
	for _, o := range thing(t, thingRow{}).Operations() {
		if o.Pattern() == "GET /api/v2/thing-kinds" {
			for _, pa := range o.Params {
				if pa.Name == "page[limit]" {
					limit = pa.Schema.(map[string]any)
				}
			}
		}
	}
	max, def := limit["maximum"].(int), limit["default"].(int)
	if _, err := query.Parse(url.Values{"page[limit]": {strconv.Itoa(max)}}); err != nil {
		t.Errorf("the described maximum %d is refused: %v", max, err)
	}
	if _, err := query.Parse(url.Values{"page[limit]": {strconv.Itoa(max + 1)}}); err == nil {
		t.Errorf("%d is accepted, the described maximum is %d", max+1, max)
	}
	if p, _ := query.Parse(url.Values{}); p.Limit != def {
		t.Errorf("an unpaged list gets %d, the description says %d", p.Limit, def)
	}
}

// the 401 the description promises (with WWW-Authenticate) is the host's
func TestOperations_UnauthorizedIsProblemWithChallenge(t *testing.T) {
	h := entitytest.Srv(t, thing(t, thingRow{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v2/thing-kinds", nil))
	if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") == "" || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
}
