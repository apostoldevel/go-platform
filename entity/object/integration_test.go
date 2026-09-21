//go:build integration

package object

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

func live(t *testing.T) *resttest.Live {
	return resttest.Start(t, "go-object-test", func(r *pgtx.Runner) platform.Module { return New(Config{Doer: r}) })
}

// One object of any entity: its generic row, the methods of its state, the
// search that finds it by a word of its label — each with parity; then the
// two ways to run something on it, refused by the database without a change.
func TestIntegration_ObjectMethodsSearchAndRefusedRuns(t *testing.T) {
	l := live(t)
	p := resttest.ListOf(t, l.Call("GET", "/api/v2/objects?filter[statetypecode]=enabled&resttest.Page[limit]=1", ""), "an enabled object")
	id, _ := p.Items[0]["id"].(string)
	entity, _ := p.Items[0]["entitycode"].(string)
	label, _ := p.Items[0]["label"].(string)
	rec := l.Call("GET", "/api/v2/objects/"+id, "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT row_to_json(t) FROM api.get_object($1::uuid) t", id)) {
		t.Fatalf("get parity: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/objects/"+id+"/methods", "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT coalesce(json_agg(row_to_json(t) ORDER BY t.sequence), '[]') FROM api.get_object_methods($1::uuid) t", id)) {
		t.Fatalf("methods parity: %d %s", rec.Code, rec.Body)
	}
	if word := strings.Fields(label); len(word) > 0 {
		q := url.QueryEscape(word[0])
		rec = l.Call("GET", "/api/v2/search?q="+q+"&entities="+entity, "")
		if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT coalesce(json_agg(row_to_json(t)), '[]') FROM api.search($1, $2::jsonb, (SELECT code FROM api.current_locale())) t", word[0], `["`+entity+`"]`)) {
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
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT coalesce(json_agg(row_to_json(t)), '[]') FROM api.object_access($1::uuid) t", id)) {
		t.Fatalf("entries parity: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/objects/"+id+"/access/decode", "")
	var bits struct{ S, U, D *bool }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &bits) != nil || bits.S == nil || !*bits.S || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT row_to_json(t) FROM api.decode_object_access($1::uuid, api.current_userid()) t", id)) {
		t.Fatalf("decode parity: %d %s", rec.Code, rec.Body)
	}
	var me struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	_ = json.Unmarshal(l.Direct(t, "SELECT json_build_object('user', row_to_json(t)) FROM api.current_user() t"), &me)
	rec = l.Call("GET", "/api/v2/objects/"+id+"/access/decode?userid="+me.User.ID, "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT row_to_json(t) FROM api.decode_object_access($1::uuid, $2::uuid) t", id, me.User.ID)) {
		t.Fatalf("decode by userid: %d %s", rec.Code, rec.Body)
	}
	// a grant of the full mask to the caller, then the caller's own entry
	// put back as it was (mask 0 deletes the row — so only when there was none)
	var entries []struct {
		UserID string `json:"userid"`
		Type   string `json:"type"`
		Mask   int    `json:"mask"`
	}
	_ = json.Unmarshal(l.Direct(t, "SELECT coalesce(json_agg(row_to_json(t)), '[]') FROM api.object_access($1::uuid) t", id), &entries)
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
	if !resttest.SameJSON([]byte(`[`+string(mustJSON(page.Items[0]))+`]`), l.Direct(t, "SELECT json_agg(json_build_object('file', t.file, 'name', t.name, 'size', t.size)) FROM api.list_object_file($1::jsonb, NULL, NULL, NULL, NULL) t", `[{"field":"object","compare":"EQL","value":"`+id+`"}]`)) {
		t.Fatalf("list parity: %s", rec.Body)
	}
	rec = l.Call("GET", "/api/v2/objects/"+id+"/files/"+file, "")
	var got map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got["data"] != "aGVsbG8=" || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT row_to_json(t) FROM api.get_object_file($1::uuid, $2::uuid, NULL, NULL) t", id, file)) {
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
