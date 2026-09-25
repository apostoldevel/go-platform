//go:build integration

package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

func live(t *testing.T) *resttest.Live {
	return resttest.Start(t, "go-workflow-test", func(r *pgtx.Runner) platform.Module { return New(Config{Doer: r}) })
}

// Reads of every catalogue with parity of one row each; the constructor's
// collections scoped to one class as the panel does it.
func TestIntegration_CataloguesAndClassScopedReads(t *testing.T) {
	l := live(t)
	for _, res := range []struct {
		path, getFn string
	}{{"entities", "api.get_entity"}, {"actions", "api.get_action"}, {"priorities", "api.get_priority"}, {"classes", "api.get_class"}, {"types", "api.get_type"}, {"states", "api.get_state"}, {"methods", "api.get_method"}, {"transitions", "api.get_transition"}, {"events", "api.get_event"}} {
		p := resttest.ListOf(t, l.Call("GET", "/api/v2/"+res.path+"?page[limit]=3", ""), res.path)
		id, _ := p.Items[0]["id"].(string)
		rec := l.Call("GET", "/api/v2/"+res.path+"/"+id, "")
		if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT row_to_json(t) FROM "+res.getFn+"($1::uuid) t", id)) {
			t.Fatalf("%s/%s parity: %d %s", res.path, id, rec.Code, rec.Body)
		}
		if rec.Header().Get("ETag") == "" {
			t.Fatalf("%s: no ETag", res.path)
		}
	}
	// a concrete class (one that has a type) and what the constructor shows for it
	aType := resttest.ListOf(t, l.Call("GET", "/api/v2/types?page[limit]=1", ""), "a type")
	class, _ := aType.Items[0]["class"].(string)
	for sub, sort := range map[string]string{"types": "code", "states": "sequence", "methods": "sequence", "events": "sequence"} {
		resttest.ListOf(t, l.Call("GET", "/api/v2/"+sub+"?filter[class]="+class+"&sort="+sort, ""), sub+" of the class")
	}
	// rights of the class: who holds what, and the caller's own bits decoded
	rec := l.Call("GET", "/api/v2/classes/"+class+"/access", "")
	if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "[") || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT coalesce(json_agg(row_to_json(t)), '[]') FROM api.class_access($1::uuid) t", class)) {
		t.Fatalf("class access: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/classes/"+class+"/access/decode", "")
	var bits map[string]bool
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &bits) != nil || len(bits) != 5 {
		t.Fatalf("class access decode: %d %s", rec.Code, rec.Body)
	}
	methods := resttest.ListOf(t, l.Call("GET", "/api/v2/methods?filter[class]="+class+"&page[limit]=1", ""), "one method")
	method, _ := methods.Items[0]["id"].(string)
	rec = l.Call("GET", "/api/v2/methods/"+method+"/access/decode", "")
	bits = nil
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &bits) != nil || len(bits) != 3 {
		t.Fatalf("method access decode: %d %s", rec.Code, rec.Body)
	}
	// the two type catalogues: a view, read as the pool's role — see admin's area-types
	var granted bool
	_ = json.Unmarshal(l.Direct(t, "SELECT to_json(has_table_privilege(current_user, 'api.state_type', 'SELECT'))"), &granted)
	rec = l.Call("GET", "/api/v2/state-types", "")
	if granted && (rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT coalesce(json_agg(row_to_json(t)), '[]') FROM api.state_type t"))) {
		t.Fatalf("state-types: %d %s", rec.Code, rec.Body)
	}
	if !granted {
		t.Log("api.state_type is not granted to the pool's role — GET /api/v2/state-types and /event-types answer 500 until the database grants SELECT on api.* views")
	}
	stateTypes := resttest.ListOf(t, l.Call("GET", "/api/v2/states?page[limit]=1", ""), "a state")
	st, _ := stateTypes.Items[0]["type"].(string)
	if rec = l.Call("GET", "/api/v2/state-types/"+st, ""); rec.Code != 200 {
		t.Fatalf("state-types/{id}: %d %s", rec.Code, rec.Body)
	}
}

// A type of a class is the cheapest thing the constructor creates: create →
// parity → patch with ETag → delete → 404; leaves nothing behind.
func TestIntegration_TypeLifecycle(t *testing.T) {
	l := live(t)
	classes := resttest.ListOf(t, l.Call("GET", "/api/v2/classes?filter[code]=client", ""), "class client")
	class, _ := classes.Items[0]["id"].(string)
	code := fmt.Sprintf("go-test-%d.client", time.Now().UnixNano())

	rec := l.Call("POST", "/api/v2/types", `{"class":"`+class+`","code":"`+code+`","name":"Go test type"}`, "Idempotency-Key", code)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if id == "" || rec.Header().Get("Location") != "/api/v2/types/"+id {
		t.Fatalf("created: %s %v", rec.Body, rec.Header())
	}
	t.Cleanup(func() { l.Call("DELETE", "/api/v2/types/"+id, "") })
	if again := l.Call("POST", "/api/v2/types", `{"class":"`+class+`","code":"`+code+`","name":"Go test type"}`, "Idempotency-Key", code); again.Code != 201 || again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay: %d %v", again.Code, again.Header())
	}
	rec = l.Call("GET", "/api/v2/types/"+id, "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT row_to_json(t) FROM api.get_type($1::uuid) t", id)) {
		t.Fatalf("parity: %d %s", rec.Code, rec.Body)
	}
	if st := l.Call("PATCH", "/api/v2/types/"+id, `{"name":"Renamed"}`, "If-Match", `W/"stale"`); st.Code != 412 {
		t.Fatalf("412: %d %s", st.Code, st.Body)
	}
	rec = l.Call("PATCH", "/api/v2/types/"+id, `{"name":"Renamed"}`, "If-Match", rec.Header().Get("ETag"))
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if rec.Code != 200 || created["name"] != "Renamed" {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("DELETE", "/api/v2/types/"+id, ""); rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("GET", "/api/v2/types/"+id, ""); rec.Code != 404 {
		t.Fatalf("after delete: %d %s", rec.Code, rec.Body)
	}
}
