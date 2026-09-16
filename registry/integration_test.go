//go:build integration

package registry

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

func live(t *testing.T) *resttest.Live {
	return resttest.Start(t, "go-registry-test", func(r *pgtx.Runner) platform.Module { return New(Config{Doer: r}) })
}

func TestIntegration_RegistryLifecycle(t *testing.T) {
	l := live(t)
	sub := fmt.Sprintf(`go-test\%d`, time.Now().UnixNano())
	ref := "key=CURRENT_USER&subkey=" + url.QueryEscape(sub)
	t.Cleanup(func() { l.Call("DELETE", "/api/v2/registry/tree?"+ref, "") })

	// three typed values under a fresh subkey of the user's hive
	for name, tv := range map[string][2]any{"n": {"integer", 42}, "s": {"string", "hello"}, "b": {"boolean", true}} {
		body, _ := json.Marshal(map[string]any{"key": "CURRENT_USER", "subkey": sub, "name": name, "type": tv[0], "value": tv[1]})
		rec := l.Call("PUT", "/api/v2/registry/values", string(body))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"`+name+`"`) {
			t.Fatalf("write %s: %d %s", name, rec.Code, rec.Body)
		}
	}
	// one read back, typed
	rec := l.Call("GET", "/api/v2/registry/values/read?"+ref+"&name=n", "")
	var v struct {
		VType    int  `json:"vtype"`
		VInteger *int `json:"vinteger"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || rec.Code != 200 || v.VType != 0 || v.VInteger == nil || *v.VInteger != 42 {
		t.Fatalf("read: %d %s", rec.Code, rec.Body)
	}
	// enumerated: three values, parity with api.registry_enum_value_ex
	rec = l.Call("GET", "/api/v2/registry/values?"+ref+"&extended=true", "")
	var values []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &values); err != nil || rec.Code != 200 || len(values) != 3 {
		t.Fatalf("enum values: %d %s", rec.Code, rec.Body)
	}
	if !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT coalesce(json_agg(row_to_json(t)), '[]') FROM api.registry_enum_value_ex($1, $2) t", "CURRENT_USER", sub)) {
		t.Fatalf("enum parity: %s", rec.Body)
	}
	// the key tree as the panel reads it; the path of the value's key
	rec = l.Call("GET", "/api/v2/registry/keys", "")
	var tree []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &tree); err != nil || rec.Code != 200 || len(tree) == 0 {
		t.Fatalf("keys: %d %s", rec.Code, rec.Body)
	}
	keyID, _ := tree[0]["id"].(string)
	if rec = l.Call("GET", "/api/v2/registry/keys/"+keyID+"/path", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"path":`) {
		t.Fatalf("key path: %d %s", rec.Code, rec.Body)
	}
	// delete one value by (key, subkey, name), the rest with the tree
	if rec = l.Call("DELETE", "/api/v2/registry/values?"+ref+"&name=s", ""); rec.Code != 204 {
		t.Fatalf("delete value: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/registry/values?"+ref, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &values)
	if rec.Code != 200 || len(values) != 2 {
		t.Fatalf("after delete: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("DELETE", "/api/v2/registry/tree?"+ref, ""); rec.Code != 204 {
		t.Fatalf("delete tree: %d %s", rec.Code, rec.Body)
	}
}
