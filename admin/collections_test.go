package admin

import (
	"net/http"
	"strings"
	"testing"
)

func TestModule_PrefixesCoverTheAdminModule(t *testing.T) {
	m := New(Config{Doer: noDB{t}})
	if got := strings.Join(m.Prefixes(), " "); got != "/api/v2/users /api/v2/groups /api/v2/areas /api/v2/area-types /api/v2/interfaces /api/v2/sessions" {
		t.Fatal(got)
	}
}

func TestCollections_RoutesAreThere(t *testing.T) {
	mux := http.NewServeMux()
	New(Config{Doer: noDB{t}}).Routes(mux)
	var want []string
	for _, c := range []string{"groups", "areas", "interfaces"} {
		want = append(want,
			"GET /api/v2/"+c, "POST /api/v2/"+c, "GET /api/v2/"+c+"/{id}", "PATCH /api/v2/"+c+"/{id}", "DELETE /api/v2/"+c+"/{id}",
			"GET /api/v2/"+c+"/{id}/members", "POST /api/v2/"+c+"/{id}/members", "DELETE /api/v2/"+c+"/{id}/members/{uid}")
	}
	want = append(want, "POST /api/v2/areas/{id}/actions/{action}", "POST /api/v2/areas/actions/clear", "GET /api/v2/area-types", "GET /api/v2/sessions")
	rep := strings.NewReplacer("{id}", "7f3a0000-0000-4000-8000-000000000001", "{uid}", "7f3a0000-0000-4000-8000-000000000002", "{action}", "delete-safely")
	for _, w := range want {
		method, pattern, _ := strings.Cut(w, " ")
		r, _ := http.NewRequest(method, "http://x"+rep.Replace(pattern), nil)
		if _, got := mux.Handler(r); got != w {
			t.Fatalf("%s → %q", w, got)
		}
	}
}

func TestCollections_CreateValidation_WithoutDB(t *testing.T) {
	h := srv(t, noDB{t})
	for path, body := range map[string]string{
		"/api/v2/groups":     `{"name":"no username"}`,
		"/api/v2/areas":      `{"name":"no code"}`,
		"/api/v2/interfaces": `{"name":"no code"}`,
	} {
		if rec := do(h, "POST", path, body); rec.Code != 400 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	if rec := do(h, "POST", "/api/v2/groups", `{"username":"g","name":"n","nope":1}`); rec.Code != 400 {
		t.Fatalf("unknown key: %d %s", rec.Code, rec.Body)
	}
}

func TestCollections_MembersValidation_WithoutDB(t *testing.T) {
	h := srv(t, noDB{t})
	if rec := do(h, "POST", "/api/v2/groups/7f3a0000-0000-4000-8000-000000000001/members", `{"id":"nope"}`); rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "DELETE", "/api/v2/areas/7f3a0000-0000-4000-8000-000000000001/members/nope", ""); rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "PATCH", "/api/v2/interfaces/7f3a0000-0000-4000-8000-000000000001", `{"name":"x"}`); rec.Code != 428 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestAreas_UnknownActionIs404_WithoutDB(t *testing.T) {
	if rec := do(srv(t, noDB{t}), "POST", "/api/v2/areas/7f3a0000-0000-4000-8000-000000000001/actions/explode", ""); rec.Code != 404 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// PATCH is partial: absent fields keep their value, but a present required
// field must not be emptied — and that is decided before the database.
func TestCollections_PatchRefusesEmptyRequiredField_WithoutDB(t *testing.T) {
	h := srv(t, noDB{t})
	for path, body := range map[string]string{
		"/api/v2/groups/7f3a0000-0000-4000-8000-000000000001":     `{"username":""}`,
		"/api/v2/areas/7f3a0000-0000-4000-8000-000000000001":      `{"code":""}`,
		"/api/v2/interfaces/7f3a0000-0000-4000-8000-000000000001": `{"code":""}`,
	} {
		if rec := do(h, "PATCH", path, body, "If-Match", `W/"x"`); rec.Code != 400 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
}

// Every PATCH of /api/v2 decides in the same order: id, then If-Match, then body.
func TestPatch_BadIDBeforePrecondition(t *testing.T) {
	h := srv(t, noDB{t})
	for _, path := range []string{"/api/v2/users/nope", "/api/v2/users/nope/profile", "/api/v2/groups/nope"} {
		if rec := do(h, "PATCH", path, `{"name":"x"}`); rec.Code != 400 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestChangePassword_EmptyOldIs400_WithoutDB(t *testing.T) {
	if rec := do(srv(t, noDB{t}), "POST", uid+"/actions/change-password", `{"old":"","new":"x"}`); rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
