//go:build integration

package admin

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

func TestIntegration_GroupsLifecycleAndParity(t *testing.T) {
	l := live(t)
	name := fmt.Sprintf("go-test-group-%d", time.Now().UnixNano())

	rec := l.Call("POST", "/api/v2/groups", `{"username":"`+name+`","name":"Go test group"}`)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id: %s", rec.Body)
	}
	t.Cleanup(func() { l.Call("DELETE", "/api/v2/groups/"+id, "") })

	rec = l.Call("GET", "/api/v2/groups/"+id, "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Row(t, "get_group", pgtx.Args{"id": id})) {
		t.Fatalf("get/parity: %d %s", rec.Code, rec.Body)
	}
	etag := rec.Header().Get("ETag")
	rec = l.Call("PATCH", "/api/v2/groups/"+id, `{"username":"`+name+`","name":"Renamed"}`, "If-Match", etag)
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if rec.Code != 200 || created["name"] != "Renamed" {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}

	// members: the administrator joins the group, is listed, leaves
	var me struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(l.Row(t, "get_user", nil), &me)
	if rec = l.Call("POST", "/api/v2/groups/"+id+"/members", `{"id":"`+me.ID+`"}`); rec.Code != 204 {
		t.Fatalf("member add: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/groups/"+id+"/members", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), me.ID) {
		t.Fatalf("members: %d %s", rec.Code, rec.Body)
	}
	if !resttest.SameJSON(rec.Body.Bytes(), l.Rows(t, "group_member", pgtx.Args{"groupid": id})) {
		t.Fatalf("members parity: %s", rec.Body)
	}
	if rec = l.Call("DELETE", "/api/v2/groups/"+id+"/members/"+me.ID, ""); rec.Code != 204 {
		t.Fatalf("member delete: %d %s", rec.Code, rec.Body)
	}

	rec = l.Call("GET", "/api/v2/groups?page[limit]=2&fields=id,username", "")
	var list struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != 200 || list.Total < 1 || len(list.Items) == 0 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	if rec = l.Call("DELETE", "/api/v2/groups/"+id, ""); rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("GET", "/api/v2/groups/"+id, ""); rec.Code != 404 {
		t.Fatalf("after delete: %d %s", rec.Code, rec.Body)
	}
}

func TestIntegration_AreasAndInterfaces(t *testing.T) {
	l := live(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())

	// area types and locales: the module calls api.list_area_type and
	// api.list_locale (by code) and no longer reads the views api.area_type /
	// api.locale, so there is no privilege on a view to probe — the
	// references are the same functions with the module's arguments
	if rec := l.Call("GET", "/api/v2/area-types", ""); rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Rows(t, "list_area_type", pgtx.Args{"limit": 0})) {
		t.Fatalf("area-types: %d %s", rec.Code, rec.Body)
	}
	if rec := l.Call("GET", "/api/v2/locales", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"code":"en"`) || !resttest.SameJSON(rec.Body.Bytes(), l.Rows(t, "list_locale", pgtx.Args{"orderby": json.RawMessage(`["code ASC"]`), "limit": 0})) {
		t.Fatalf("locales: %d %s", rec.Code, rec.Body)
	}
	rec := l.Call("GET", "/api/v2/areas?page[limit]=5", "")
	var list struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != 200 || len(list.Items) == 0 {
		t.Fatalf("areas: %d %s", rec.Code, rec.Body)
	}
	parent, _ := list.Items[0]["id"].(string)

	// an area under the first one: create → get parity → delete-safely → gone
	rec = l.Call("POST", "/api/v2/areas", `{"parent":"`+parent+`","code":"go-test-`+stamp+`","name":"Go test area"}`)
	if rec.Code != 201 {
		t.Fatalf("area create: %d %s", rec.Code, rec.Body)
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	aid, _ := created["id"].(string)
	t.Cleanup(func() { l.Call("DELETE", "/api/v2/areas/"+aid, "") })
	if rec = l.Call("GET", "/api/v2/areas/"+aid, ""); rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Row(t, "get_area", pgtx.Args{"id": aid})) {
		t.Fatalf("area get/parity: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("POST", "/api/v2/areas/"+aid+"/actions/delete-safely", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"deleted":true`) {
		t.Fatalf("delete-safely: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("GET", "/api/v2/areas/"+aid, ""); rec.Code != 404 {
		t.Fatalf("after delete-safely: %d %s", rec.Code, rec.Body)
	}

	// interface: create → patch → members (self) → delete
	rec = l.Call("POST", "/api/v2/interfaces", `{"code":"go-test-`+stamp+`","name":"Go test interface"}`)
	if rec.Code != 201 {
		t.Fatalf("interface create: %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	iid, _ := created["id"].(string)
	t.Cleanup(func() { l.Call("DELETE", "/api/v2/interfaces/"+iid, "") })
	rec = l.Call("PATCH", "/api/v2/interfaces/"+iid, `{"code":"go-test-`+stamp+`","name":"Renamed"}`, "If-Match", rec.Header().Get("ETag"))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Renamed") {
		t.Fatalf("interface patch: %d %s", rec.Code, rec.Body)
	}
	var me struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(l.Row(t, "get_user", nil), &me)
	if rec = l.Call("POST", "/api/v2/interfaces/"+iid+"/members", `{"id":"`+me.ID+`"}`); rec.Code != 204 {
		t.Fatalf("interface member add: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("GET", "/api/v2/interfaces/"+iid+"/members", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), me.ID) {
		t.Fatalf("interface members: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("DELETE", "/api/v2/interfaces/"+iid+"/members/"+me.ID, ""); rec.Code != 204 {
		t.Fatalf("interface member delete: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("DELETE", "/api/v2/interfaces/"+iid, ""); rec.Code != 204 {
		t.Fatalf("interface delete: %d %s", rec.Code, rec.Body)
	}

	// sessions: the list sees this very session
	rec = l.Call("GET", "/api/v2/sessions?filter[username]=admin&page[limit]=5", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != 200 || list.Total < 1 {
		t.Fatalf("sessions: %d %s", rec.Code, rec.Body)
	}
}
