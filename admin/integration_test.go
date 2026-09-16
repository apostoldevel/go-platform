//go:build integration

package admin

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
	return resttest.Start(t, "go-admin-test", func(r *pgtx.Runner) platform.Module { return New(Config{Doer: r}) })
}

func TestIntegration_UsersLifecycleAndParity(t *testing.T) {
	l := live(t)
	name := fmt.Sprintf("go-test-%d", time.Now().UnixNano())

	// list = api.list_user + api.count_user
	rec := l.Call("GET", "/api/v2/users?page[limit]=3&sort=-created&fields=id,username", "")
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var list struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || list.Total < 1 || len(list.Items) == 0 || list.Items[0]["username"] == nil || list.Items[0]["email"] != nil {
		t.Fatalf("list body: %v %s", err, rec.Body)
	}
	var total int64
	_ = json.Unmarshal(l.Direct(t, "SELECT api.count_user(NULL)"), &total)
	if total != list.Total {
		t.Fatalf("total %d, api.count_user %d", list.Total, total)
	}

	// create
	rec = l.Call("POST", "/api/v2/users", `{"username":"`+name+`","password":"Go-test-1!","name":"Go test","email":"`+name+`@example.test"}`, "Idempotency-Key", name)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if id != "" {
		t.Cleanup(func() { l.Call("DELETE", "/api/v2/users/"+id, "") })
	}
	if id == "" || rec.Header().Get("Location") != "/api/v2/users/"+id || rec.Header().Get("ETag") == "" {
		t.Fatalf("create headers: %v id=%s", rec.Header(), id)
	}
	if again := l.Call("POST", "/api/v2/users", `{"username":"`+name+`","password":"Go-test-1!","name":"Go test","email":"`+name+`@example.test"}`, "Idempotency-Key", name); again.Code != 201 || again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay: %d %v", again.Code, again.Header())
	}

	// get: parity with api.get_user, ETag, 304
	rec = l.Call("GET", "/api/v2/users/"+id, "")
	if rec.Code != 200 {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	want := l.Direct(t, "SELECT row_to_json(t) FROM api.get_user($1::uuid) t", id)
	if !resttest.SameJSON(rec.Body.Bytes(), want) {
		t.Fatalf("parity:\n v2 %s\n v1 %s", rec.Body.Bytes(), want)
	}
	etag := rec.Header().Get("ETag")
	if nm := l.Call("GET", "/api/v2/users/"+id, "", "If-None-Match", etag); nm.Code != 304 {
		t.Fatalf("304: %d", nm.Code)
	}

	// patch: stale ETag → 412, right → 200 with the change
	if st := l.Call("PATCH", "/api/v2/users/"+id, `{"name":"Renamed"}`, "If-Match", `W/"stale"`); st.Code != 412 {
		t.Fatalf("412: %d %s", st.Code, st.Body)
	}
	rec = l.Call("PATCH", "/api/v2/users/"+id, `{"name":"Renamed"}`, "If-Match", etag)
	var patched map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if rec.Code != 200 || patched["name"] != "Renamed" || rec.Header().Get("ETag") == etag {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	etag = rec.Header().Get("ETag")

	// profile
	rec = l.Call("PATCH", "/api/v2/users/"+id+"/profile", `{"given_name":"Go","family_name":"Test"}`, "If-Match", etag)
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if rec.Code != 200 || patched["given_name"] != "Go" || patched["family_name"] != "Test" {
		t.Fatalf("profile: %d %s", rec.Code, rec.Body)
	}

	// lock / unlock
	rec = l.Call("POST", "/api/v2/users/"+id+"/actions/lock", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if rec.Code != 200 || !strings.Contains(fmt.Sprint(patched["statustext"]), "locked") {
		t.Fatalf("lock: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("POST", "/api/v2/users/"+id+"/actions/unlock", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if rec.Code != 200 || strings.Contains(fmt.Sprint(patched["statustext"]), "locked") {
		t.Fatalf("unlock: %d %s", rec.Code, rec.Body)
	}

	// iptable round trip
	rec = l.Call("PUT", "/api/v2/users/"+id+"/iptable", `{"allow":["10.0.0.1-10.0.0.5","192.168.1.1"],"deny":[]}`)
	if rec.Code != 200 {
		t.Fatalf("iptable put: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/users/"+id+"/iptable", "")
	var ipt iptable
	_ = json.Unmarshal(rec.Body.Bytes(), &ipt)
	if rec.Code != 200 || len(ipt.Allow) != 2 || len(ipt.Deny) != 0 {
		t.Fatalf("iptable get: %d %s", rec.Code, rec.Body)
	}

	// memberships: groups add → listed → delete; areas/interfaces readable; parity with api.member_group
	var group struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(l.Direct(t, "SELECT row_to_json(t) FROM api.list_group(NULL, NULL, 1) t"), &group)
	if group.ID == "" {
		t.Fatal("no group to test membership with")
	}
	if rec = l.Call("POST", "/api/v2/users/"+id+"/groups", `{"id":"`+group.ID+`"}`); rec.Code != 204 {
		t.Fatalf("group add: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/users/"+id+"/groups", "")
	var groups []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &groups)
	found := false
	for _, g := range groups {
		found = found || g["id"] == group.ID
	}
	if rec.Code != 200 || !found {
		t.Fatalf("groups: %d %s", rec.Code, rec.Body)
	}
	var v1 []map[string]any
	_ = json.Unmarshal(l.Direct(t, "SELECT coalesce(json_agg(row_to_json(t)), '[]') FROM api.member_group($1::uuid) t", id), &v1)
	if len(v1) != len(groups) {
		t.Fatalf("parity groups: v2 %d, api.member_group %d", len(groups), len(v1))
	}
	if rec = l.Call("DELETE", "/api/v2/users/"+id+"/groups/"+group.ID, ""); rec.Code != 204 {
		t.Fatalf("group delete: %d %s", rec.Code, rec.Body)
	}
	for _, sub := range []string{"areas", "interfaces", "memberships"} {
		if rec = l.Call("GET", "/api/v2/users/"+id+"/"+sub, ""); rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "[") {
			t.Fatalf("%s: %d %s", sub, rec.Code, rec.Body)
		}
	}

	// unknown action, foreign id
	if rec = l.Call("POST", "/api/v2/users/"+id+"/actions/explode", ""); rec.Code != 404 {
		t.Fatalf("explode: %d", rec.Code)
	}
	if rec = l.Call("GET", "/api/v2/users/00000000-0000-4000-b000-00000000dead", ""); rec.Code != 404 {
		t.Fatalf("foreign: %d %s", rec.Code, rec.Body)
	}

	// delete → 204 → 404
	if rec = l.Call("DELETE", "/api/v2/users/"+id, ""); rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("GET", "/api/v2/users/"+id, ""); rec.Code != 404 {
		t.Fatalf("after delete: %d %s", rec.Code, rec.Body)
	}
}
