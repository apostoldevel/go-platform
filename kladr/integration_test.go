//go:build integration

package kladr

import (
	"encoding/json"
	"testing"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

func live(t *testing.T) *resttest.Live {
	return resttest.Start(t, "go-kladr-test", func(r resttest.Doer) platform.Module { return New(Config{Doer: r}) })
}

// The tree is empty on every known database: the shapes are checked, and
// the parity of what the functions answer for a node that is not there.
func TestIntegration_ShapesOnAnEmptyTree(t *testing.T) {
	l := live(t)
	rec := l.Call("GET", "/api/v2/kladr?page[limit]=1", "")
	var page resttest.Page
	var total int64
	_ = json.Unmarshal(l.Row(t, "count_address_tree", nil), &total)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || page.Total != total || page.Items == nil {
		t.Fatalf("list: %d %s (count %d)", rec.Code, rec.Body, total)
	}
	if rec = l.Call("GET", "/api/v2/kladr/2147483647", ""); rec.Code != 404 {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/kladr/2147483647/history", "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Rows(t, "get_address_tree_history", pgtx.Args{"id": int32(2147483647)})) {
		t.Fatalf("history: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/kladr/string?code=7700000000000&short=1", "")
	// the v1 value wrapped as the module wraps it: {"address": <the scalar>}
	want, _ := json.Marshal(map[string]json.RawMessage{"address": l.Row(t, "get_address_tree_string", pgtx.Args{"code": "7700000000000", "short": 1, "level": 0})})
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), want) {
		t.Fatalf("string: %d %s", rec.Code, rec.Body)
	}
}
