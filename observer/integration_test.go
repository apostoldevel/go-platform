//go:build integration

package observer

import (
	"encoding/json"
	"strings"
	"testing"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

func live(t *testing.T) *resttest.Live {
	return resttest.Start(t, "go-observer-test", func(r *pgtx.Runner) platform.Module { return New(Config{Doer: r}) })
}

func TestIntegration_Publishers(t *testing.T) {
	l := live(t)
	rec := l.Call("GET", "/api/v2/observer/publishers", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"code":"notify"`) || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT coalesce(json_agg(row_to_json(t) ORDER BY code), '[]') FROM api.publisher t")) {
		t.Fatalf("publishers: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/observer/publishers/notify", "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT row_to_json(t) FROM api.get_publisher($1) t", "notify")) {
		t.Fatalf("parity: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("GET", "/api/v2/observer/publishers/no_such_publisher", ""); rec.Code != 404 {
		t.Fatalf("unknown: %d %s", rec.Code, rec.Body)
	}
}

// A subscription made the WebSocket way (api.subscribe_observer in the
// caller's session) is what /listeners shows — without the session code.
func TestIntegration_ListenersOfMySession(t *testing.T) {
	l := live(t)
	l.Direct(t, "SELECT to_json(count(*)) FROM api.subscribe_observer('notify', NULL, 'go_test', '{\"classes\":[\"client\"]}'::jsonb, '{\"type\":\"object\"}'::jsonb)")
	t.Cleanup(func() { l.Direct(t, "SELECT to_json(api.unsubscribe_observer('notify', NULL, 'go_test'))") })
	rec := l.Call("GET", "/api/v2/observer/listeners", "")
	var mine []map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &mine) != nil || len(mine) != 1 || mine[0]["publisher"] != "notify" || mine[0]["identity"] != "go_test" {
		t.Fatalf("listeners: %d %s", rec.Code, rec.Body)
	}
	if _, leaked := mine[0]["session"]; leaked || strings.Contains(rec.Body.String(), l.Session.Code) {
		t.Fatal("the session code is on the wire")
	}
	rec = l.Call("GET", "/api/v2/observer/listeners/notify/go_test", "")
	var one map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &one) != nil || one["filter"].(map[string]any)["classes"] == nil || rec.Header().Get("ETag") == "" {
		t.Fatalf("listener: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("GET", "/api/v2/observer/listeners/notify/main", ""); rec.Code != 404 {
		t.Fatalf("not mine: %d %s", rec.Code, rec.Body)
	}
}
