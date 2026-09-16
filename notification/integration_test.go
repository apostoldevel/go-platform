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
	return resttest.Start(t, "go-notification-test", func(r *pgtx.Runner) platform.Module { return New(Config{Doer: r}) })
}

// The newest notification: read by id with parity; its object comes back
// from /changed as the entity's own row; /since answers an array.
func TestIntegration_ListGetSinceChanged(t *testing.T) {
	l := live(t)
	p := resttest.ListOf(t, l.Call("GET", "/api/v2/notifications?sort=-datetime&resttest.Page[limit]=20", ""), "notifications")
	// the newest notification whose object still exists (the newest of all may be a drop)
	var id, object, entity string
	for _, n := range p.Items {
		object, _ = n["object"].(string)
		if !resttest.SameJSON(l.Direct(t, "SELECT to_json(count(*) > 0) FROM api.get_object($1::uuid)", object), []byte("true")) {
			continue
		}
		id, _ = n["id"].(string)
		entity, _ = n["entitycode"].(string)
		break
	}
	if id == "" {
		t.Skip("none of the 20 newest notifications points at a living object")
	}
	rec := l.Call("GET", "/api/v2/notifications/"+id, "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT row_to_json(t) FROM api.get_notification($1::uuid) t", id)) {
		t.Fatalf("get parity: %d %s", rec.Code, rec.Body)
	}
	// the last hour only: the journal is millions of rows on a working database
	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	rec = l.Call("GET", "/api/v2/notifications/since?from="+from, "")
	var since []map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &since) != nil {
		t.Fatalf("since: %d %s", rec.Code, rec.Body)
	}
	// the journal grows while the test runs: the later api.* answer is a superset of the earlier v2 one
	var direct []map[string]any
	_ = json.Unmarshal(l.Direct(t, "SELECT coalesce(json_agg(row_to_json(t)), '[]') FROM api.notification($1::timestamptz) t", from), &direct)
	ids := map[any]bool{}
	for _, d := range direct {
		ids[d["id"]] = true
	}
	for _, n := range since {
		if !ids[n["id"]] {
			t.Fatalf("since parity: %v not in api.notification (%d vs %d rows)", n["id"], len(since), len(direct))
		}
	}
	rec = l.Call("GET", "/api/v2/notifications/changed?objects="+object+"&from=2000-01-01T00:00:00Z", "")
	var changed []json.RawMessage
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &changed) != nil || len(changed) != 1 {
		t.Fatalf("changed: %d %s", rec.Code, rec.Body)
	}
	if !resttest.SameJSON(changed[0], l.Direct(t, "SELECT row_to_json(t) FROM api.get_"+entity+"($1::uuid) t", object)) {
		t.Fatalf("changed parity (%s): %s", entity, changed[0])
	}
	// a period before any notification: no objects, still an array
	if rec = l.Call("GET", "/api/v2/notifications/changed?objects="+object+"&to=2000-01-01T00:00:00Z", ""); rec.Code != 200 || rec.Body.String() != "[]" {
		t.Fatalf("changed empty: %d %s", rec.Code, rec.Body)
	}
}
