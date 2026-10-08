package query

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
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
	// a name with a character outside a-z is the client's own (JSON:API
	// "implementation-specific"): a cache-buster or tracking parameter must
	// not break the request
	for _, raw := range []string{"_=1", "utm_source=x", "cacheBust=1", "x-trace=1", "_=1&filter[state]=enabled"} {
		if _, err := Parse(mustQuery(t, raw)); err != nil {
			t.Errorf("%q: %v", raw, err)
		}
	}
}

func TestUnknownListParametersRefused(t *testing.T) {
	// a name of a-z only is reserved for the list language; an unknown one
	// is a typo of it, and ignoring it answers "everything" to a selection
	for raw, name := range map[string]string{
		"limit=50":              "limit",
		"offset=100":            "offset",
		"search=ivan":           "search",
		"order=name":            "order",
		"filters[state]=x":      "filters[state]",
		"fields[client]=id":     "fields[client]", // no sparse fieldsets by type
		"sort[name]=asc":        "sort[name]",
		"_=1&limit=50":          "limit",
		"filter[state]=x&q=abc": "q",
	} {
		_, err := Parse(mustQuery(t, raw))
		if err == nil {
			t.Errorf("%q accepted", raw)
			continue
		}
		if !strings.Contains(err.Error(), strconv.Quote(name)) {
			t.Errorf("%q: %v — the refusal must name %q", raw, err, name)
		}
	}
}

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
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
