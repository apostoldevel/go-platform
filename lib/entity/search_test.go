package entity

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/apostoldevel/go-platform/lib/query"
)

var fields = []string{"name", "code", "email", "phone"}

// q= is the one thing lib/query does not express: one term over several
// fields joined by OR, in brackets so that the filter[…] conditions beside
// it keep their AND. The shape is api.sql()'s search array (condition,
// field, compare, value, lstr, rstr — CheckJsonbKeys in api.sql).
func TestSearchOf_TermBecomesBracketedORGroup(t *testing.T) {
	p, err := query.Parse(url.Values{"filter[statetypecode]": {"enabled"}})
	if err != nil {
		t.Fatal(err)
	}
	got := SearchOf(p, "  Volt ", fields)
	var conds []map[string]any
	if err := json.Unmarshal(got, &conds); err != nil {
		t.Fatal(err)
	}
	// filter first (AND), then the group: ( name OR code OR email OR phone )
	if len(conds) != 1+len(fields) {
		t.Fatalf("%d conditions: %s", len(conds), got)
	}
	if conds[0]["field"] != "statetypecode" || conds[0]["compare"] != "EQL" || conds[0]["value"] != "enabled" {
		t.Fatalf("filter lost: %v", conds[0])
	}
	for i, c := range conds[1:] {
		if c["field"] != fields[i] || c["compare"] != "IKE" || c["value"] != "%Volt%" {
			t.Fatalf("group[%d] = %v", i, c)
		}
		_, or := c["condition"]
		_, l := c["lstr"]
		_, r := c["rstr"]
		if (i == 0) != l || (i == len(fields)-1) != r || (i > 0) != or {
			t.Fatalf("group[%d] brackets/condition = %v", i, c)
		}
		if or && c["condition"] != "OR" {
			t.Fatalf("group[%d] condition = %v", i, c["condition"])
		}
	}
}

// One field makes a group of one: both brackets on the same element.
func TestSearchOf_OneFieldIsBracketedAlone(t *testing.T) {
	p, _ := query.Parse(url.Values{})
	var conds []map[string]any
	if err := json.Unmarshal(SearchOf(p, "x", []string{"name"}), &conds); err != nil {
		t.Fatal(err)
	}
	if len(conds) != 1 || conds[0]["lstr"] != "(" || conds[0]["rstr"] != ")" || conds[0]["condition"] != nil {
		t.Fatalf("%v", conds)
	}
}

// Without a term the search is exactly what lib/query translated — nil when
// there is nothing, so api.sql() applies its defaults. A term over no fields
// is nothing as well, not a broken group.
func TestSearchOf_NoTermIsTheFilterAlone(t *testing.T) {
	p, _ := query.Parse(url.Values{})
	if got := SearchOf(p, "", fields); got != nil {
		t.Fatalf("expected nil, got %s", got)
	}
	if got := SearchOf(p, "term", nil); got != nil {
		t.Fatalf("expected nil for no fields, got %s", got)
	}
	p, _ = query.Parse(url.Values{"filter[code]": {"V-1"}})
	got := SearchOf(p, "   ", fields)
	if string(got) != `[{"field":"code","compare":"EQL","value":"V-1"}]` {
		t.Fatalf("got %s", got)
	}
}

// LIKE wildcards in the term are data, not pattern: a "%" typed by a user
// must not turn "find this" into "find everything".
func TestSearchOf_EscapesWildcards(t *testing.T) {
	p, _ := query.Parse(url.Values{})
	var conds []map[string]any
	if err := json.Unmarshal(SearchOf(p, `50%_x\y`, fields), &conds); err != nil {
		t.Fatal(err)
	}
	if conds[0]["value"] != `%50\%\_x\\y%` {
		t.Fatalf("value = %q", conds[0]["value"])
	}
}

func TestKnownKeys(t *testing.T) {
	ok := url.Values{"filter[name]": {"x"}, "page[limit]": {"5"}, "sort": {"name"}, "q": {"x"}}
	if err := KnownKeys(ok, "sort", "fields", "q"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"limit", "Q", "filtr[name]", "filter", "page"} {
		if err := KnownKeys(url.Values{bad: {"1"}}, "sort", "fields", "q"); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}

// q= is this layer's word, not the list language's: lib/query refuses an
// unknown name of a-z (JSON:API reserves them, lib/query), so q= is
// taken out before Parse — and its term must survive the taking out (read
// after the delete, it is empty and the selection silently turns into
// "everything", the very failure lib/query refuses).
func TestListArgs_QSurvivesTheExactingParse(t *testing.T) {
	values := url.Values{"q": {"GO-1"}, "filter[statetypecode]": {"enabled"}, "page[limit]": {"5"}}
	p, search, err := listArgs(values, fields)
	if err != nil {
		t.Fatal(err)
	}
	if p.Limit != 5 {
		t.Fatalf("limit = %d", p.Limit)
	}
	var conds []map[string]string
	if err := json.Unmarshal(search, &conds); err != nil {
		t.Fatal(err)
	}
	if len(conds) != 1+len(fields) || conds[0]["field"] != "statetypecode" || conds[1]["value"] != "%GO-1%" {
		t.Fatalf("search = %s", search)
	}
	if values.Get("q") != "GO-1" {
		t.Fatal("the caller's values were changed")
	}
}

func TestListArgs_QOnlyWhereThereAreFields(t *testing.T) {
	if _, _, err := listArgs(url.Values{"q": {"x"}}, nil); err == nil {
		t.Fatal("q= accepted by an entity without SearchFields")
	}
	for _, bad := range []string{"limit=5", "query=x", "filters[name]=x", "q=a&q=b"} {
		v, _ := url.ParseQuery(bad)
		if _, _, err := listArgs(v, fields); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}
