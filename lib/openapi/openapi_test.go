package openapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

type inner struct {
	Deep string `json:"deep"`
}

type sample struct {
	inner
	ID      string     `json:"id" format:"uuid" doc:"The id."`
	Name    *string    `json:"name" doc:"A name."`
	Count   int32      `json:"count"`
	Big     int64      `json:"big,omitempty"`
	When    time.Time  `json:"when"`
	Maybe   *time.Time `json:"maybe"`
	Kind    string     `json:"kind" enum:"a,b"`
	Tags    []string   `json:"tags"`
	Raw     any        `json:"raw"`
	Code    *string    `json:"code" required:"create"`
	Skipped string     `json:"-"`
	hidden  string
}

func schemaOf(t *testing.T, mode Mode) map[string]any {
	t.Helper()
	doc, err := Build(Info{Title: "t", Version: "1"}, []Operation{{
		Method: "POST", Path: "/x", ID: "x", Responses: []Response{{Status: "200", Description: "ok", Schema: Schema{Name: "S", Value: sample{}, Mode: mode}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return doc["components"].(map[string]any)["schemas"].(map[string]any)["S"].(map[string]any)
}

func TestSchema_FromTags(t *testing.T) {
	s := schemaOf(t, Row)
	props := s["properties"].(map[string]any)
	for _, k := range []string{"deep", "id", "name", "count", "big", "when", "maybe", "kind", "tags", "raw", "code"} {
		if _, ok := props[k]; !ok {
			t.Errorf("property %s missing: %v", k, props)
		}
	}
	for _, k := range []string{"Skipped", "hidden", "-", ""} {
		if _, ok := props[k]; ok {
			t.Errorf("property %q must not be there", k)
		}
	}
	want := map[string]map[string]any{
		"id":    {"type": "string", "format": "uuid", "description": "The id."},
		"name":  {"type": []any{"string", "null"}, "description": "A name."},
		"count": {"type": "integer", "format": "int32"},
		"when":  {"type": "string", "format": "date-time"},
		"maybe": {"type": []any{"string", "null"}, "format": "date-time"},
		"kind":  {"type": "string", "enum": []any{"a", "b"}},
		"raw":   {},
		"tags":  {"type": "array", "items": map[string]any{"type": "string"}},
	}
	for k, w := range want {
		if !reflect.DeepEqual(props[k], w) {
			t.Errorf("%s: %v, want %v", k, props[k], w)
		}
	}
	// a Row has all its columns: required are the ones without omitempty
	req := s["required"].([]any)
	if !contains(req, "id") || !contains(req, "name") || contains(req, "big") {
		t.Errorf("row required: %v", req)
	}
}

func TestSchema_CreateAndPatch(t *testing.T) {
	c := schemaOf(t, Create)
	if got := c["required"]; !reflect.DeepEqual(got, []any{"code"}) {
		t.Errorf("create required: %v", got)
	}
	if c["additionalProperties"] != false {
		t.Errorf("a body refuses unknown keys: %v", c["additionalProperties"])
	}
	// a required key is neither null nor empty (rest.Required refuses both)
	code := c["properties"].(map[string]any)["code"].(map[string]any)
	if code["type"] != "string" || code["minLength"] != 1 {
		t.Errorf("required create key must be a non-empty, non-null string: %v", code)
	}
	if pc := schemaOf(t, Patch)["properties"].(map[string]any)["code"].(map[string]any); pc["type"] == "string" {
		t.Errorf("patch keeps null (do not change): %v", pc)
	}
	p := schemaOf(t, Patch)
	if _, ok := p["required"]; ok {
		t.Errorf("patch requires none: %v", p["required"])
	}
	if p["minProperties"] != 1 || p["additionalProperties"] != false {
		t.Errorf("patch: %v", p)
	}
}

func contains(a []any, s string) bool {
	for _, e := range a {
		if e == s {
			return true
		}
	}
	return false
}

func op(method, path, id string) Operation {
	return Operation{Method: method, Path: path, ID: id, Responses: []Response{{Status: "200", Description: "ok"}}}
}

func TestBuild_Refusals(t *testing.T) {
	idParam := Param{Name: "id", In: "path", Schema: map[string]any{"type": "string"}}
	cases := map[string][]Operation{
		"same operationId":          {op("GET", "/a", "x"), op("GET", "/b", "x")},
		"same method and path":      {op("GET", "/a", "x"), op("GET", "/a", "y")},
		"path parameter undeclared": {op("GET", "/a/{id}", "x")},
		"declared, not in the path": {func() Operation { o := op("GET", "/a", "x"); o.Params = []Param{idParam}; return o }()},
		"no responses":              {{Method: "GET", Path: "/a", ID: "x"}},
		"no id":                     {{Method: "GET", Path: "/a", Responses: []Response{{Status: "200", Description: "ok"}}}},
		"bad parameter place":       {func() Operation { o := op("GET", "/a", "x"); o.Params = []Param{{Name: "q", In: "cookie"}}; return o }()},
	}
	for name, ops := range cases {
		if _, err := Build(Info{Title: "t", Version: "1"}, ops); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// control: the same shapes done right pass
	ok := op("GET", "/a/{id}", "x")
	ok.Params = []Param{idParam}
	if _, err := Build(Info{Title: "t", Version: "1"}, []Operation{ok, op("GET", "/b", "y")}); err != nil {
		t.Fatal(err)
	}
}

// one name for two different schemas would make one of them silently wrong
func TestBuild_SchemaNameIsOneSchema(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("no panic")
		}
	}()
	type other struct {
		A string `json:"a"`
	}
	a, b := op("GET", "/a", "a"), op("GET", "/b", "b")
	a.Responses[0].Schema = Schema{Name: "S", Value: sample{}}
	b.Responses[0].Schema = Schema{Name: "S", Value: other{}}
	_, _ = Build(Info{Title: "t", Version: "1"}, []Operation{a, b})
}

func TestBuild_ProblemComponentAndDeterminism(t *testing.T) {
	ops := []Operation{op("GET", "/b", "b"), op("POST", "/a", "a"), op("GET", "/a", "c")}
	ops[0].Responses[0] = Response{Status: "404", Problem: true, Description: "no"}
	var first []byte
	for i := 0; i < 5; i++ {
		doc, err := Build(Info{Title: "t", Version: "1"}, ops)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = b
		} else if !bytes.Equal(first, b) {
			t.Fatal("two builds of one input differ")
		}
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(first, &doc); err != nil {
		t.Fatal(err)
	}
	// the Problem component is the wire type of lib/problem
	for _, k := range []string{"type", "title", "status", "detail", "instance", "request_id", "code"} {
		if _, ok := doc.Components.Schemas["Problem"].Properties[k]; !ok {
			t.Errorf("Problem.%s missing", k)
		}
	}
	if !strings.HasSuffix(string(first), "}\n") {
		t.Error("no trailing newline")
	}
}

func muxOf(patterns ...string) *http.ServeMux {
	mux := http.NewServeMux()
	for _, p := range patterns {
		mux.HandleFunc(p, func(http.ResponseWriter, *http.Request) {})
	}
	return mux
}

// Gaps in both directions, each with its mutant: a route the mux has and the
// description lacks, a route the description has and the mux lacks.
func TestGaps(t *testing.T) {
	ops := []Operation{op("GET", "/a", "1"), op("POST", "/a", "2"), op("GET", "/a/{id}", "3")}
	full := []string{"GET /a", "POST /a", "GET /a/{id}"}

	if m, u := Gaps(muxOf(full...), ops); len(m)+len(u) != 0 {
		t.Fatalf("a true description has gaps: %v %v", m, u)
	}
	// mutant 1: a route without a description (a DELETE beside the documented ones)
	m, u := Gaps(muxOf(append([]string{"DELETE /a/{id}"}, full...)...), ops)
	if len(m) != 0 || !reflect.DeepEqual(u, []string{"DELETE /a/{id}"}) {
		t.Fatalf("undescribed route not seen: %v %v", m, u)
	}
	// mutant 2: a described route the mux does not serve
	m, u = Gaps(muxOf("GET /a", "GET /a/{id}"), ops)
	if !reflect.DeepEqual(m, []string{"POST /a"}) || len(u) != 0 {
		t.Fatalf("unmounted route not seen: %v %v", m, u)
	}
	// mutant 3: the route exists under another method
	m, _ = Gaps(muxOf("GET /a", "PUT /a", "GET /a/{id}"), ops)
	if !reflect.DeepEqual(m, []string{"POST /a"}) {
		t.Fatalf("a PUT must not stand in for a POST: %v", m)
	}
}
