//go:build integration

package object

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

func live(t *testing.T) *resttest.Live {
	return resttest.Start(t, "go-object-test", func(r resttest.Doer) platform.Module { return New(Config{Doer: r}) })
}

// One object of any entity: its generic row, the methods of its state, the
// search that finds it by a word of its label — each with parity; then the
// two ways to run something on it, refused by the database without a change.
func TestIntegration_ObjectMethodsSearchAndRefusedRuns(t *testing.T) {
	l := live(t)
	p := resttest.ListOf(t, l.Call("GET", "/api/v2/objects?filter[statetypecode]=enabled&page[limit]=1", ""), "an enabled object")
	id, _ := p.Items[0]["id"].(string)
	entity, _ := p.Items[0]["entitycode"].(string)
	label, _ := p.Items[0]["label"].(string)
	rec := l.Call("GET", "/api/v2/objects/"+id, "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Row(t, "get_object", pgtx.Args{"id": id})) {
		t.Fatalf("get parity: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/objects/"+id+"/methods", "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), bySequence(t, l.Rows(t, "get_object_methods", pgtx.Args{"object": id}))) {
		t.Fatalf("methods parity: %d %s", rec.Code, rec.Body)
	}
	if word := strings.Fields(label); len(word) > 0 {
		q := url.QueryEscape(word[0])
		rec = l.Call("GET", "/api/v2/search?q="+q+"&entities="+entity, "")
		// the locale of the session, as (SELECT code FROM api.current_locale()) gave it
		var locale struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(l.Row(t, "current_locale", nil), &locale)
		want := l.Rows(t, "search", pgtx.Args{"text": word[0], "entities": json.RawMessage(`["` + entity + `"]`), "localecode": locale.Code})
		if rec.Code != 200 || locale.Code == "" || !resttest.SameJSON(rec.Body.Bytes(), want) {
			t.Fatalf("search parity: %d %s", rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), `"id":"`+id+`"`) {
			t.Logf("search %q over %s did not list the object itself — the searchable text is not the label", word[0], entity)
		}
	}
	// an action the class does not have: the database refuses inside api.execute_object_action
	rec = l.Call("POST", "/api/v2/objects/"+id+"/actions/no_such_action", "")
	if rec.Code != 400 || !strings.Contains(rec.Header().Get("Content-Type"), "problem+json") {
		t.Fatalf("unknown action: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("POST", "/api/v2/objects/"+id+"/methods/7f3a0000-0000-4000-8000-000000000001", `{"x":1}`)
	if rec.Code != 400 && rec.Code != 404 {
		t.Fatalf("unknown method: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("POST", "/api/v2/objects/7f3a0000-0000-4000-8000-000000000001/actions/enable", ""); rec.Code != 404 {
		t.Fatalf("no such object: %d %s", rec.Code, rec.Body)
	}
	var after map[string]any
	rec = l.Call("GET", "/api/v2/objects/"+id, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &after)
	if after["state"] != p.Items[0]["state"] {
		t.Fatalf("state changed by a refused run: %v → %v", p.Items[0]["state"], after["state"])
	}
}

// Access of one object: the entries, the bits of the caller decoded (parity
// with api.decode_object_access), a grant to the caller and back; a
// missing object is 404, not three NULLs.
func TestIntegration_ObjectAccess(t *testing.T) {
	l := live(t)
	p := resttest.ListOf(t, l.Call("GET", "/api/v2/objects?filter[statetypecode]=enabled&page[limit]=1", ""), "an enabled object")
	id, _ := p.Items[0]["id"].(string)
	rec := l.Call("GET", "/api/v2/objects/"+id+"/access", "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Rows(t, "object_access", pgtx.Args{"id": id})) {
		t.Fatalf("entries parity: %d %s", rec.Code, rec.Body)
	}
	// the caller: api.current_user()'s id — api.current_userid() is not in
	// daemon.call's allow list, and for the session's own user they are one
	var me struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	_ = json.Unmarshal(l.Row(t, "current_user", nil), &me.User)
	if me.User.ID == "" {
		t.Fatal("api.current_user: no id")
	}
	rec = l.Call("GET", "/api/v2/objects/"+id+"/access/decode", "")
	var bits struct{ S, U, D *bool }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &bits) != nil || bits.S == nil || !*bits.S || !resttest.SameJSON(rec.Body.Bytes(), l.Row(t, "decode_object_access", pgtx.Args{"id": id, "userid": me.User.ID})) {
		t.Fatalf("decode parity: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/objects/"+id+"/access/decode?userid="+me.User.ID, "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Row(t, "decode_object_access", pgtx.Args{"id": id, "userid": me.User.ID})) {
		t.Fatalf("decode by userid: %d %s", rec.Code, rec.Body)
	}
	// a grant of the full mask to the caller, then the caller's own entry
	// put back as it was (mask 0 deletes the row — so only when there was none)
	var entries []struct {
		UserID string `json:"userid"`
		Type   string `json:"type"`
		Mask   int    `json:"mask"`
	}
	_ = json.Unmarshal(l.Rows(t, "object_access", pgtx.Args{"id": id}), &entries)
	restore := `{"userid":"` + me.User.ID + `","mask":0}`
	for _, e := range entries {
		if e.UserID == me.User.ID && e.Type == "U" {
			restore = `{"userid":"` + me.User.ID + `","mask":` + strconv.Itoa(e.Mask) + `}`
		}
	}
	t.Cleanup(func() { l.Call("PUT", "/api/v2/objects/"+id+"/access", restore) })
	rec = l.Call("PUT", "/api/v2/objects/"+id+"/access", `{"userid":"`+me.User.ID+`","mask":7}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"userid":"`+me.User.ID+`"`) {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("PUT", "/api/v2/objects/"+id+"/access", restore); rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/access", "/access/decode"} {
		if rec = l.Call("GET", "/api/v2/objects/7f3a0000-0000-4000-8000-000000000001"+path, ""); rec.Code != 404 {
			t.Fatalf("no such object %s: %d %s", path, rec.Code, rec.Body)
		}
	}
}

// Files of one object: none, one added (the row back, then listed with
// total and read with its bytes), deleted (204, then 404), the list
// cleared; a foreign or missing object is 404 on every route.
func TestIntegration_ObjectFiles(t *testing.T) {
	l := live(t)
	// an object that has no files: the test must not wipe anyone's attachments
	p := resttest.ListOf(t, l.Call("GET", "/api/v2/objects?filter[statetypecode]=enabled&page[limit]=20", ""), "enabled objects")
	id, page := "", resttest.Page{}
	for _, it := range p.Items {
		cand, _ := it["id"].(string)
		rec := l.Call("GET", "/api/v2/objects/"+cand+"/files", "")
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil {
			t.Fatalf("list: %d %s", rec.Code, rec.Body)
		}
		if page.Total == 0 && len(page.Items) == 0 {
			id = cand
			break
		}
	}
	if id == "" {
		t.Skip("every enabled object of the first page has files")
	}
	t.Cleanup(func() { l.Call("DELETE", "/api/v2/objects/"+id+"/files", "") })
	var rec *httptest.ResponseRecorder
	body := `[{"name":"go-object-test.txt","size":5,"date":"2026-01-02T03:04:05Z","data":"aGVsbG8=","type":"text/plain"}]`
	rec = l.Call("POST", "/api/v2/objects/"+id+"/files", body)
	var rows []map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &rows) != nil || len(rows) != 1 || rows[0]["name"] != "go-object-test.txt" {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	// bad base64 is the client's (class 22 → 400), not an internal error
	if rec = l.Call("POST", "/api/v2/objects/"+id+"/files", `[{"name":"bad.bin","data":"not base64!"}]`); rec.Code != 400 {
		t.Fatalf("bad base64: %d %s", rec.Code, rec.Body)
	}
	// clear then add again: DELETE …/files leaves no rows, the add is an upsert by name
	if rec = l.Call("DELETE", "/api/v2/objects/"+id+"/files", ""); rec.Code != 204 {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("POST", "/api/v2/objects/"+id+"/files", body); rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &rows) != nil || len(rows) != 1 {
		t.Fatalf("add after clear: %d %s", rec.Code, rec.Body)
	}
	file, _ := rows[0]["file"].(string)
	rec = l.Call("GET", "/api/v2/objects/"+id+"/files?filter[type]=text/plain&fields=file,name,size", "")
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0]["file"] != file || page.Items[0]["path"] != nil {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	// v1: api.list_object_file by the object, projected to the three fields
	// asked for (what json_build_object('file', 'name', 'size') did in SQL)
	var v1 []struct {
		File any `json:"file"`
		Name any `json:"name"`
		Size any `json:"size"`
	}
	_ = json.Unmarshal(l.Rows(t, "list_object_file", pgtx.Args{"search": json.RawMessage(`[{"field":"object","compare":"EQL","value":"` + id + `"}]`)}), &v1)
	if !resttest.SameJSON([]byte(`[`+string(mustJSON(page.Items[0]))+`]`), mustJSON(v1)) {
		t.Fatalf("list parity: %s", rec.Body)
	}
	rec = l.Call("GET", "/api/v2/objects/"+id+"/files/"+file, "")
	var got map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got["data"] != "aGVsbG8=" || !resttest.SameJSON(rec.Body.Bytes(), l.Row(t, "get_object_file", pgtx.Args{"object": id, "file": file, "name": nil, "path": nil})) {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("DELETE", "/api/v2/objects/"+id+"/files/"+file, ""); rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("DELETE", "/api/v2/objects/"+id+"/files/"+file, ""); rec.Code != 404 {
		t.Fatalf("delete again: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("GET", "/api/v2/objects/"+id+"/files/"+file, ""); rec.Code != 404 {
		t.Fatalf("get after delete: %d %s", rec.Code, rec.Body)
	}
	for _, c := range [][2]string{{"GET", "/files"}, {"POST", "/files"}, {"DELETE", "/files"}, {"GET", "/files/" + file}, {"DELETE", "/files/" + file}} {
		if rec = l.Call(c[0], "/api/v2/objects/7f3a0000-0000-4000-8000-000000000001"+c[1], body); rec.Code != 404 {
			t.Fatalf("no such object %s %s: %d %s", c[0], c[1], rec.Code, rec.Body)
		}
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// bySequence is a JSON array of rows ordered by their "sequence", stably —
// what ORDER BY t.sequence did over the rows in SQL.
func bySequence(t *testing.T, rows json.RawMessage) []byte {
	t.Helper()
	var items []map[string]any
	if err := json.Unmarshal(rows, &items); err != nil {
		t.Fatalf("rows: %v", err)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, _ := items[i]["sequence"].(float64)
		b, _ := items[j]["sequence"].(float64)
		return a < b
	})
	if items == nil {
		return []byte("[]")
	}
	return mustJSON(items)
}
