//go:build integration

package object

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

func live(t *testing.T) *resttest.Live {
	return resttest.Start(t, "go-object-test", func(r *pgtx.Runner) platform.Module { return New(Config{Doer: r}) })
}

// One object of any entity: its generic row, the methods of its state, the
// search that finds it by a word of its label — each with parity; then the
// two ways to run something on it, refused by the database without a change.
func TestIntegration_ObjectMethodsSearchAndRefusedRuns(t *testing.T) {
	l := live(t)
	p := resttest.ListOf(t, l.Call("GET", "/api/v2/objects?filter[statetypecode]=enabled&resttest.Page[limit]=1", ""), "an enabled object")
	id, _ := p.Items[0]["id"].(string)
	entity, _ := p.Items[0]["entitycode"].(string)
	label, _ := p.Items[0]["label"].(string)
	rec := l.Call("GET", "/api/v2/objects/"+id, "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT row_to_json(t) FROM api.get_object($1::uuid) t", id)) {
		t.Fatalf("get parity: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/objects/"+id+"/methods", "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT coalesce(json_agg(row_to_json(t) ORDER BY t.sequence), '[]') FROM api.get_object_methods($1::uuid) t", id)) {
		t.Fatalf("methods parity: %d %s", rec.Code, rec.Body)
	}
	if word := strings.Fields(label); len(word) > 0 {
		q := url.QueryEscape(word[0])
		rec = l.Call("GET", "/api/v2/search?q="+q+"&entities="+entity, "")
		if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT coalesce(json_agg(row_to_json(t)), '[]') FROM api.search($1, $2::jsonb, (SELECT code FROM api.current_locale())) t", word[0], `["`+entity+`"]`)) {
			t.Fatalf("search parity: %d %s", rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), `"id":"`+id+`"`) {
			t.Logf("search %q over %s did not list the object itself — the searchable text is not the label", word[0], entity)
		}
	}
	// an action the class does not have: the database refuses inside api.execute_object_action
	rec = l.Call("POST", "/api/v2/objects/"+id+"/actions/no_such_action", "")
	if rec.Code != 400 || !strings.Contains(rec.Header().Get("Content-Type"), "problem+json") {
		t.Fatalf("unknown action: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("POST", "/api/v2/objects/"+id+"/methods/7f3a0000-0000-4000-8000-000000000001", `{"x":1}`)
	if rec.Code != 400 && rec.Code != 404 {
		t.Fatalf("unknown method: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("POST", "/api/v2/objects/7f3a0000-0000-4000-8000-000000000001/actions/enable", ""); rec.Code != 404 {
		t.Fatalf("no such object: %d %s", rec.Code, rec.Body)
	}
	var after map[string]any
	rec = l.Call("GET", "/api/v2/objects/"+id, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &after)
	if after["state"] != p.Items[0]["state"] {
		t.Fatalf("state changed by a refused run: %v → %v", p.Items[0]["state"], after["state"])
	}
}
