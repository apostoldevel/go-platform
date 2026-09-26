//go:build integration

package notification

import (
	"encoding/json"
	"testing"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

func live(t *testing.T) *resttest.Live {
	return resttest.Start(t, "go-notification-test", func(r resttest.Doer) platform.Module { return New(Config{Doer: r}) })
}

// The newest notification: read by id with parity; its object comes back
// from /changed as the entity's own row; /since answers an array.
func TestIntegration_ListGetSinceChanged(t *testing.T) {
	l := live(t)
	p := resttest.ListOf(t, l.Call("GET", "/api/v2/notifications?sort=-datetime&page[limit]=100", ""), "notifications")
	// the newest notification whose object the caller reads through its
	// entity's own api.get_<x> — what /changed answers with. api.get_object is
	// not that test: it sees a document outside the caller's area tree (a
	// company's own area, say) that api.get_<x> rightly hides, and /changed
	// then rightly leaves out; the newest of all may also be a drop. A
	// passed-over object that api.get_object still sees is kept: /changed
	// must leave it out. A hundred, not twenty: a run that creates companies
	// fills the top of the journal with objects the caller does not read.
	// The scan goes on until both are found: the first readable object (the
	// positive side) and the first hidden one (the negative side).
	var id, object, entity, hidden string
	for _, n := range p.Items {
		if id != "" && hidden != "" {
			break
		}
		o, _ := n["object"].(string)
		e, _ := n["entitycode"].(string)
		fn, ok := getFnOf(e)
		if ok && readable(t, l, fn, o) {
			if id == "" {
				id, _ = n["id"].(string)
				object, entity = o, e
			}
			continue
		}
		if hidden == "" && len(rows(t, l, "get_object", o)) > 0 {
			hidden = o
		}
	}
	// the negative side is its own subtest, so that its absence shows in the
	// report instead of passing in silence: go-platform cannot make the
	// hidden object itself (a company is a configuration's entity)
	t.Run("hidden object is left out", func(t *testing.T) {
		if hidden == "" {
			t.Skip("no object among the 100 newest notifications that api.get_object sees and api.get_<x> hides — the negative side is not checked")
		}
		// an object the caller may not read through its entity is left out, as in v1
		if rec := l.Call("GET", "/api/v2/notifications/changed?objects="+hidden+"&from=2000-01-01T00:00:00Z", ""); rec.Code != 200 || rec.Body.String() != "[]" {
			t.Fatalf("changed shows an object api.get_<x> hides (%s): %d %s", hidden, rec.Code, rec.Body)
		}
	})
	if id == "" {
		t.Skip("none of the 100 newest notifications points at an object the caller reads")
	}
	// the module reads the caller's own notifications: api.get_my_notification
	rec := l.Call("GET", "/api/v2/notifications/"+id, "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Row(t, "get_my_notification", pgtx.Args{"id": id})) {
		t.Fatalf("get parity: %d %s", rec.Code, rec.Body)
	}
	// the last hour only: the journal is millions of rows on a working database
	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	rec = l.Call("GET", "/api/v2/notifications/since?from="+from, "")
	var since []map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &since) != nil {
		t.Fatalf("since: %d %s", rec.Code, rec.Body)
	}
	// the journal grows while the test runs: the later api.* answer is a superset of the earlier v2 one;
	// the module's /since is api.my_notification(from), the caller's own
	var direct []map[string]any
	_ = json.Unmarshal(l.Rows(t, "my_notification", pgtx.Args{"datefrom": from}), &direct)
	ids := map[any]bool{}
	for _, d := range direct {
		ids[d["id"]] = true
	}
	for _, n := range since {
		if !ids[n["id"]] {
			t.Fatalf("since parity: %v not in api.my_notification (%d vs %d rows)", n["id"], len(since), len(direct))
		}
	}
	rec = l.Call("GET", "/api/v2/notifications/changed?objects="+object+"&from=2000-01-01T00:00:00Z", "")
	var changed []json.RawMessage
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &changed) != nil || len(changed) != 1 {
		t.Fatalf("changed: %d %s", rec.Code, rec.Body)
	}
	if !resttest.SameJSON(changed[0], l.Row(t, "get_"+entity, pgtx.Args{"id": object})) {
		t.Fatalf("changed parity (%s): %s", entity, changed[0])
	}
	// a period before any notification: no objects, still an array
	if rec = l.Call("GET", "/api/v2/notifications/changed?objects="+object+"&to=2000-01-01T00:00:00Z", ""); rec.Code != 200 || rec.Body.String() != "[]" {
		t.Fatalf("changed empty: %d %s", rec.Code, rec.Body)
	}
}

// rows is fn(id) through the module's road, failing the test on a refusal.
func rows(t *testing.T, l *resttest.Live, fn, id string) []json.RawMessage {
	t.Helper()
	out, err := l.Try(fn, pgtx.Args{"id": id})
	if err != nil {
		t.Fatalf("%s: %v", fn, err)
	}
	return out
}

// readable is what count(*) > 0 … WHERE t.id IS NOT NULL said in SQL: fn(id)
// has a row with an id — the caller reads the object through its entity.
func readable(t *testing.T, l *resttest.Live, fn, id string) bool {
	t.Helper()
	for _, row := range rows(t, l, fn, id) {
		var r struct {
			ID *string `json:"id"`
		}
		if json.Unmarshal(row, &r) == nil && r.ID != nil {
			return true
		}
	}
	return false
}
