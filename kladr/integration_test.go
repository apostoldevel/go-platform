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
	return resttest.Start(t, "go-kladr-test", func(r *pgtx.Runner) platform.Module { return New(Config{Doer: r}) })
}

// The tree is empty on every known database: the shapes are checked, and
// the parity of what the functions answer for a node that is not there.
func TestIntegration_ShapesOnAnEmptyTree(t *testing.T) {
	l := live(t)
	rec := l.Call("GET", "/api/v2/kladr?resttest.Page[limit]=1", "")
	var page resttest.Page
	var total int64
	_ = json.Unmarshal(l.Direct(t, "SELECT to_json(c) FROM api.count_address_tree() c"), &total)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || page.Total != total || page.Items == nil {
		t.Fatalf("list: %d %s (count %d)", rec.Code, rec.Body, total)
	}
	if rec = l.Call("GET", "/api/v2/kladr/2147483647", ""); rec.Code != 404 {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/kladr/2147483647/history", "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT coalesce(json_agg(row_to_json(t)), '[]') FROM api.get_address_tree_history(2147483647) t")) {
		t.Fatalf("history: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/kladr/string?code=7700000000000&short=1", "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT json_build_object('address', api.get_address_tree_string('7700000000000', 1, 0))")) {
		t.Fatalf("string: %d %s", rec.Code, rec.Body)
	}
}
