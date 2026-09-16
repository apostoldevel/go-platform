//go:build integration

package resource

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
	return resttest.Start(t, "go-resource-test", func(r *pgtx.Runner) platform.Module { return New(Config{Doer: r}) })
}

func TestIntegration_ResourceLifecycle(t *testing.T) {
	l := live(t)
	name := fmt.Sprintf("go-test-%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]any{"name": name, "type": "text", "data": "hello", "description": "Go test resource"})
	rec := l.Call("POST", "/api/v2/resources", string(body))
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if id == "" || rec.Header().Get("Location") != "/api/v2/resources/"+id {
		t.Fatalf("created: %s %v", rec.Body, rec.Header())
	}
	t.Cleanup(func() { l.Call("DELETE", "/api/v2/resources/"+id, "") })
	rec = l.Call("GET", "/api/v2/resources/"+id, "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT row_to_json(t) FROM api.get_resource($1::uuid) t", id)) {
		t.Fatalf("parity: %d %s", rec.Code, rec.Body)
	}
	etag := rec.Header().Get("ETag")
	rec = l.Call("PATCH", "/api/v2/resources/"+id, `{"data":"changed"}`, "If-Match", etag)
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if rec.Code != 200 || created["data"] != "changed" {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/resources?filter[name]="+name, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("DELETE", "/api/v2/resources/"+id, ""); rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("GET", "/api/v2/resources/"+id, ""); rec.Code != 404 {
		t.Fatalf("after delete: %d %s", rec.Code, rec.Body)
	}
}
