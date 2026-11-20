package openapi

import (
	"encoding/json"
	"strings"
	"testing"
)

type conformRow struct {
	ID    string   `json:"id" format:"uuid"`
	Name  *string  `json:"name"`
	Count int32    `json:"count"`
	When  string   `json:"when" format:"date-time"`
	Tags  []string `json:"tags"`
}

func conformDoc(t *testing.T) (map[string]any, any) {
	t.Helper()
	doc, err := Build(Info{Title: "t", Version: "1"}, []Operation{{
		Method: "GET", Path: "/a", ID: "a", Tag: "a",
		Responses: []Response{{Status: "200", Schema: Schema{Name: "Row", Value: conformRow{}, Mode: Row}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Marshal(doc)
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	return back, map[string]any{"$ref": "#/components/schemas/Row"}
}

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// A row that fits gives nothing; every kind of difference is named, one test
// row per kind — the instrument is held to its own mutants.
func TestConforms(t *testing.T) {
	doc, ref := conformDoc(t)
	good := `{"id":"3f0c3f9e-0000-4000-8000-000000000001","name":null,"count":3,"when":"2026-11-20T10:00:00+03:00","tags":["a"]}`
	if got := Conforms(doc, ref, decode(t, good)); len(got) != 0 {
		t.Fatalf("a good row: %v", got)
	}
	for name, c := range map[string]struct{ row, want string }{
		"extra key":     {strings.Replace(good, `"tags"`, `"zzz":1,"tags"`, 1), "has zzz"},
		"missing key":   {strings.Replace(good, `"count":3,`, ``, 1), "lacks count"},
		"wrong type":    {strings.Replace(good, `"count":3`, `"count":"3"`, 1), "is string, the schema says integer"},
		"fraction":      {strings.Replace(good, `"count":3`, `"count":3.5`, 1), "is number, the schema says integer"},
		"null not null": {strings.Replace(good, `"count":3`, `"count":null`, 1), "is null"},
		"bad uuid":      {strings.Replace(good, `3f0c3f9e-0000-4000-8000-000000000001`, `nope`, 1), "is not a uuid"},
		"bad time":      {strings.Replace(good, `2026-11-20T10:00:00+03:00`, `yesterday`, 1), "is not a date-time"},
		"bad item":      {strings.Replace(good, `["a"]`, `["a",1]`, 1), "$.tags[1]"},
	} {
		got := Conforms(doc, ref, decode(t, c.row))
		if len(got) == 0 || !strings.Contains(strings.Join(got, ";"), c.want) {
			t.Errorf("%s: %v, want %q", name, got, c.want)
		}
	}
	if got := Conforms(doc, map[string]any{"$ref": "#/components/schemas/Nope"}, 1); len(got) != 1 {
		t.Errorf("a dangling $ref: %v", got)
	}
}

// The branches the Build output reaches that the row above does not: enum,
// oneOf, additionalProperties as a schema, a $ref inside a $ref, a timestamp
// that is not RFC 3339, a number with a fraction.
func TestConformsBranches(t *testing.T) {
	doc := map[string]any{"components": map[string]any{"schemas": map[string]any{
		"Inner": decode(t, `{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false}`),
		"Outer": decode(t, `{"type":"object","properties":{"in":{"$ref":"#/components/schemas/Inner"}},"required":["in"]}`),
	}}}
	check := func(name, schema, value string, wantFit bool, want string) {
		t.Helper()
		got := Conforms(doc, decode(t, schema), decode(t, value))
		if wantFit && len(got) != 0 {
			t.Errorf("%s: %v, want a fit", name, got)
		}
		if !wantFit && !strings.Contains(strings.Join(got, ";"), want) {
			t.Errorf("%s: %v, want %q", name, got, want)
		}
	}
	enum := `{"type":"string","enum":["a","b"]}`
	check("enum fit", enum, `"a"`, true, "")
	check("enum miss", enum, `"c"`, false, "is not in the enum")
	check("enum other kind", `{"enum":["1","2"]}`, `1`, false, "is not in the enum")
	oneOf := `{"oneOf":[{"type":"string"},{"type":"integer"}]}`
	check("oneOf fit", oneOf, `3`, true, "")
	check("oneOf miss", oneOf, `true`, false, "fits none of the alternatives")
	extra := `{"type":"object","additionalProperties":{"type":"integer"}}`
	check("additionalProperties fit", extra, `{"x":1,"y":2}`, true, "")
	check("additionalProperties miss", extra, `{"x":1,"y":"2"}`, false, "$.y")
	check("nested ref fit", `{"$ref":"#/components/schemas/Outer"}`, `{"in":{"n":1}}`, true, "")
	check("nested ref miss", `{"$ref":"#/components/schemas/Outer"}`, `{"in":{"n":"1"}}`, false, "$.in.n")
	check("nested ref forbids", `{"$ref":"#/components/schemas/Outer"}`, `{"in":{"n":1,"z":1}}`, false, "the schema forbids it")
	dt := `{"type":"string","format":"date-time"}`
	check("date-time fit", dt, `"2026-11-20T10:00:00.123456+00:00"`, true, "")
	check("date-time Z", dt, `"2026-11-20T10:00:00Z"`, true, "")
	for _, bad := range []string{`"2026-11-20 10:00:00+00:00"`, `"2026-11-20T10:00:00"`, `"2026-11-20T10:00:00+03"`} {
		check("date-time "+bad, dt, bad, false, "is not a date-time")
	}
	// a number with no fraction is an integer in JSON Schema, whatever its size
	check("large whole number", `{"type":"integer"}`, `1e30`, true, "")
	check("fraction", `{"type":"integer"}`, `0.5`, false, "is number")
}

func TestPageMore(t *testing.T) {
	doc := map[string]any{}
	page := PageMore(map[string]any{"type": "integer"})
	if got := Conforms(doc, page, decode(t, `{"items":[1,2],"limit":2,"offset":0,"has_more":true}`)); len(got) != 0 {
		t.Fatalf("an answer without total: %v", got)
	}
	for name, c := range map[string]struct{ body, want string }{
		"total is not named": {`{"items":[],"limit":2,"offset":0,"has_more":false,"total":0}`, "has total"},
		"has_more required":  {`{"items":[],"limit":2,"offset":0}`, "lacks has_more"},
	} {
		got := Conforms(doc, page, decode(t, c.body))
		if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), c.want) {
			t.Errorf("%s: want %q, got %v", name, c.want, got)
		}
	}
}
