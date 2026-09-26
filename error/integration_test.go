//go:build integration

package error

import (
	"encoding/json"
	"strings"
	"testing"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

func live(t *testing.T) *resttest.Live {
	return resttest.Start(t, "go-error-test", func(r *pgtx.Runner) platform.Module { return New(Config{Doer: r}) })
}

// The catalogue as the panel and lib/pgtx read it: list, one by id, one by
// code — each with parity against api.*; an unknown code is 404.
func TestIntegration_CatalogueReads(t *testing.T) {
	l := live(t)
	p := resttest.ListOf(t, l.Call("GET", "/api/v2/errors?filter[http_code]=401&page[limit]=2", ""), "errors 401")
	// the page is honoured: a lost page[limit] (a renamed key lib/query
	// ignores) came back the default 500 rows and passed unnoticed
	if len(p.Items) == 0 || len(p.Items) > 2 {
		t.Fatalf("page[limit]=2 gave %d items", len(p.Items))
	}
	id, _ := p.Items[0]["id"].(string)
	code, _ := p.Items[0]["code"].(string)
	rec := l.Call("GET", "/api/v2/errors/"+id, "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT row_to_json(t) FROM api.get_error($1::uuid) t", id)) {
		t.Fatalf("get parity: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/errors/by-code/"+code, "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT row_to_json(t) FROM api.get_error_by_code($1) t", code)) {
		t.Fatalf("by-code parity: %d %s", rec.Code, rec.Body)
	}
	if tag := rec.Header().Get("ETag"); tag == "" {
		t.Fatal("by-code: no ETag")
	} else if again := l.Call("GET", "/api/v2/errors/by-code/"+code, "", "If-None-Match", tag); again.Code != 304 {
		t.Fatalf("by-code 304: %d", again.Code)
	}
	if rec = l.Call("GET", "/api/v2/errors/by-code/ERR-999-999", ""); rec.Code != 404 || !strings.Contains(rec.Header().Get("Content-Type"), "problem+json") {
		t.Fatalf("unknown code: %d %s", rec.Code, rec.Body)
	}
}

// The catalogue has no delete (neither v1 nor api.*): one reserved test
// code per database — created on the first run, patched on every later one.
func TestIntegration_ReservedCodeCreateOrPatch(t *testing.T) {
	l := live(t)
	const code = "ERR-999-001"
	rec := l.Call("GET", "/api/v2/errors/by-code/"+code, "")
	switch rec.Code {
	case 404:
		rec = l.Call("POST", "/api/v2/errors", `{"code":"`+code+`","http_code":400,"message":"Go integration test"}`, "Idempotency-Key", code)
		if rec.Code != 201 {
			t.Fatalf("create: %d %s", rec.Code, rec.Body)
		}
		var created map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &created)
		if id, _ := created["id"].(string); rec.Header().Get("Location") != "/api/v2/errors/"+id {
			t.Fatalf("Location: %q for %s", rec.Header().Get("Location"), rec.Body)
		}
	case 200:
		var row map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &row)
		id, _ := row["id"].(string)
		if st := l.Call("PATCH", "/api/v2/errors/"+id, `{"message":"Go integration test"}`, "If-Match", `W/"stale"`); st.Code != 412 {
			t.Fatalf("412: %d %s", st.Code, st.Body)
		}
		rec = l.Call("PATCH", "/api/v2/errors/"+id, `{"message":"Go integration test"}`, "If-Match", rec.Header().Get("ETag"))
		if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT row_to_json(t) FROM api.get_error($1::uuid) t", id)) {
			t.Fatalf("patch: %d %s", rec.Code, rec.Body)
		}
	default:
		t.Fatalf("by-code: %d %s", rec.Code, rec.Body)
	}
}
