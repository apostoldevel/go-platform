package query

import (
	"encoding/json"
	"net/url"
	"testing"
)

func parse(t *testing.T, raw string) Params {
	t.Helper()
	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(v)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return p
}

func js(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestFilter_EqualityAndOperators(t *testing.T) {
	p := parse(t, "filter[state]=enabled&filter[created][gte]=2026-01-01&filter[name][ilike]=%25ivan%25&filter[email][null]=true&filter[phone][null]=false&filter[code][ne]=x")
	// conditions are ANDed; the translation orders them by parameter name
	want := `[{"field":"code","compare":"NEQ","value":"x"},{"field":"created","compare":"GEQ","value":"2026-01-01"},{"field":"email","compare":"ISN"},{"field":"name","compare":"IKE","value":"%ivan%"},{"field":"phone","compare":"INN"},{"field":"state","compare":"EQL","value":"enabled"}]`
	if got := js(p.Search); got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

func TestFilter_InBecomesValarr(t *testing.T) {
	p := parse(t, "filter[state][in]=enabled,disabled")
	if got := js(p.Search); got != `[{"field":"state","valarr":["enabled","disabled"]}]` {
		t.Fatal(got)
	}
}

func TestSort_FieldsPaging(t *testing.T) {
	p := parse(t, "sort=-created,name&fields=id,name,udate&page[limit]=50&page[offset]=100")
	if js(p.OrderBy) != `["created DESC","name ASC"]` || js(p.Fields) != `["id","name","udate"]` || p.Limit != 50 || p.Offset != 100 {
		t.Fatalf("%+v", p)
	}
}

func TestDefaults(t *testing.T) {
	p := parse(t, "")
	if p.Limit != DefaultLimit || p.Offset != 0 || p.Search != nil || p.OrderBy != nil || p.Fields != nil {
		t.Fatalf("%+v", p)
	}
}

func TestRejects(t *testing.T) {
	for _, raw := range []string{
		"filter[st.ate]=x",         // field not an identifier
		"filter[state][between]=1", // unknown operator
		"sort=-cre ated",           // field not an identifier
		"fields=id,na-me",
		"page[limit]=0",
		"page[limit]=1001",
		"page[limit]=abc",
		"page[offset]=-1",
		"page[after]=x", // cursor paging not offered yet
		"filter=plain",  // filter without a field
	} {
		v, _ := url.ParseQuery(raw)
		if _, err := Parse(v); err == nil {
			t.Errorf("%q accepted", raw)
		}
	}
}

func TestUnknownParametersIgnored(t *testing.T) {
	// a cache-buster or tracking parameter must not break the request
	if _, err := Parse(url.Values{"_": {"1"}, "utm_source": {"x"}}); err != nil {
		t.Fatal(err)
	}
}

func TestJSONArgs_NullWhenEmpty(t *testing.T) {
	p := parse(t, "")
	s, o, f := p.JSONArgs()
	if s != nil || o != nil || f != nil {
		t.Fatalf("%v %v %v — empty parts must be SQL NULL", s, o, f)
	}
	p = parse(t, "sort=name")
	_, o, _ = p.JSONArgs()
	if string(o) != `["name ASC"]` {
		t.Fatal(string(o))
	}
}
