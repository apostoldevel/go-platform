//go:build integration

package observer

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/jackc/pgx/v5"
)

func live(t *testing.T) *resttest.Live {
	return resttest.Start(t, "go-observer-test", func(r resttest.Doer) platform.Module { return New(Config{Doer: r}) })
}

func TestIntegration_Publishers(t *testing.T) {
	l := live(t)
	t.Run("list", func(t *testing.T) {
		rec := l.Call("GET", "/api/v2/observer/publishers", "")
		// skipped only for that one reason: the route's refusal is ERR-403-012 and
		// the reference, called the same way, is refused too
		if _, err := l.Try("list_publisher", pgtx.Args{"limit": 0}); err != nil && rec.Code == 403 && strings.Contains(rec.Body.String(), `"code":"ERR-403-012"`) {
			t.Skipf("api.list_publisher is not in the allow list of daemon.call (ERR-403-012, %v) — GET /api/v2/observer/publishers answers 403 until the database opens it", err)
		}
		want := l.Rows(t, "list_publisher", pgtx.Args{"orderby": json.RawMessage(`["code ASC"]`), "limit": 0})
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"code":"notify"`) || !resttest.SameJSON(rec.Body.Bytes(), want) {
			t.Fatalf("publishers: %d %s", rec.Code, rec.Body)
		}
	})
	rec := l.Call("GET", "/api/v2/observer/publishers/notify", "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Row(t, "get_publisher", pgtx.Args{"code": "notify"})) {
		t.Fatalf("parity: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("GET", "/api/v2/observer/publishers/no_such_publisher", ""); rec.Code != 404 {
		t.Fatalf("unknown: %d %s", rec.Code, rec.Body)
	}
}

// subscribe runs the fixture on the administrator's connection: the
// subscription is made the WebSocket way, which is not a road of daemon.call
// (api.subscribe_observer / unsubscribe_observer are not in its allow list),
// so the session code is passed explicitly instead of taken from the
// request's context.
func subscribe(t *testing.T, sql string, args ...any) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), os.Getenv("GO_TEST_ADMIN_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// A subscription made the WebSocket way (api.subscribe_observer in the
// caller's session) is what /listeners shows — without the session code.
func TestIntegration_ListenersOfMySession(t *testing.T) {
	l := live(t)
	subscribe(t, `SELECT count(*) FROM api.subscribe_observer('notify', $1, 'go_test', '{"classes":["client"]}'::jsonb, '{"type":"object"}'::jsonb)`, l.Session.Code)
	t.Cleanup(func() { subscribe(t, "SELECT api.unsubscribe_observer('notify', $1, 'go_test')", l.Session.Code) })
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
