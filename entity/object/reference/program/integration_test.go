//go:build integration

package program

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

func live(t *testing.T) *resttest.Live {
	return resttest.Start(t, "go-program-test", func(r resttest.Doer) platform.Module { return New(Config{Doer: r}) })
}

// seeded is the id of one row of the entity found through the generic list
// of objects (api.list_object, open to a session): the list of the entity's
// own is not in the allow list of every database (T839).
func seeded(t *testing.T, l *resttest.Live) string {
	t.Helper()
	search := `[{"field":"entitycode","compare":"EQL","value":"program"}]`
	rows, err := l.Try("list_object", pgtx.Args{"search": pgtx.JSON([]byte(search)), "limit": 1})
	if err != nil {
		t.Fatalf("list_object: %v", err)
	}
	if len(rows) == 0 {
		t.Skip("no program in the database")
	}
	var row struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rows[0], &row); err != nil || row.ID == "" {
		t.Fatalf("object row: %s", rows[0])
	}
	return row.ID
}

// refusedByAllowList says whether the database refused the call because the
// function is not in gateway.function (ERR-403-012) — and not for any other
// 403, which is a failure of its own.
func refusedByAllowList(rec *httptest.ResponseRecorder) bool {
	return rec.Code == 403 && strings.Contains(rec.Body.String(), "ERR-403-012")
}

// GET {id} on the daemon road: the row of api.get_program with its ETag,
// parity with the function called through the same road; an unknown id is
// 404, not three NULLs; a missing ETag match is 304.
func TestIntegration_ProgramGet(t *testing.T) {
	l := live(t)
	id := seeded(t, l)
	rec := l.Call("GET", Prefix+"/"+id, "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Row(t, "get_program", pgtx.Args{"id": id})) {
		t.Fatalf("get parity: %d %s", rec.Code, rec.Body)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	if nm := l.Call("GET", Prefix+"/"+id, "", "If-None-Match", etag); nm.Code != 304 {
		t.Fatalf("304: %d", nm.Code)
	}
	if rec = l.Call("GET", Prefix+"/7f3a0000-0000-4000-8000-000000000001", ""); rec.Code != 404 {
		t.Fatalf("unknown id: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("GET", Prefix+"/"+id+"/methods", ""); rec.Code != 200 && !refusedByAllowList(rec) {
		t.Fatalf("methods: %d %s", rec.Code, rec.Body)
	}
}

// GET list: {items, total, limit, offset} over api.count_program and
// api.list_program, and q= over the search fields. Both functions are in the
// allow list or the route is refused 403 by the database (ERR-403-012) —
// then the test says so and skips: the gateway's allow list is the database layer's
// (T839), not this package's.
func TestIntegration_ProgramList(t *testing.T) {
	l := live(t)
	rec := l.Call("GET", Prefix+"?page[limit]=1", "")
	if refusedByAllowList(rec) {
		t.Skipf("api.list_program / api.count_program are not in gateway.function of this database (T839): %s", rec.Body)
	}
	var p resttest.Page
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if p.Total == 0 || len(p.Items) == 0 {
		t.Skip("no program in the database")
	}
	if len(p.Items) > 1 {
		t.Fatalf("page[limit]=1: %d items", len(p.Items))
	}
	code, _ := p.Items[0]["code"].(string)
	if code == "" {
		t.Skip("the first row has no code to search for")
	}
	rec = l.Call("GET", Prefix+"?q="+url.QueryEscape(code), "")
	found := false
	var q resttest.Page
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil || rec.Code != 200 {
		t.Fatalf("q=: %d %s", rec.Code, rec.Body)
	}
	for _, it := range q.Items {
		found = found || it["code"] == code
	}
	if !found {
		t.Fatalf("q=%s did not find the row: %s", code, rec.Body)
	}
	if rec = l.Call("GET", Prefix+"?q=a&q=b", ""); rec.Code != 400 || !strings.Contains(rec.Body.String(), "more than once") {
		t.Fatalf("repeated q=: %d %s", rec.Code, rec.Body)
	}
}

// TestIntegration_ProgramWriteEcho is the third layer of the write check
// (every writable key written with a value of its own, on POST and again on
// PATCH, read back through the package and through api.get_program).
// It waits for the allow list: api.add_program and api.set_program are not in
// gateway.function and the daemon road refuses them (ERR-403-012) — the level
// of the write is the database layer's decision, card T839; a hand-made fixture of it is
// not made here. The shape of the write (the body's keys against the
// catalogue's parameters) is checked without the database by the binary's
// grid (TestGrid_WriteShape).
func TestIntegration_ProgramWriteEcho(t *testing.T) {
	t.Skip("api.add_program / api.set_program are not in gateway.function until T839 (the level of write is the database layer's)")
}
