package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/auth/jwt"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/jackc/pgx/v5"
)

// noDB fails the test if a request reaches the database.
type noDB struct{ t *testing.T }

func (n noDB) Do(context.Context, pgtx.Session, *pgtx.Request, func(context.Context, pgx.Tx) error) error {
	n.t.Fatal("database reached")
	return nil
}

var keys = jwt.Keyring{Audiences: map[string]jwt.Key{"web-test": {Alg: "HS256", Secret: []byte("s")}}, Issuers: []string{"accounts.test"}}

func token(sub string) string {
	return jwt.Sign(jwt.Claims{Iss: "accounts.test", Aud: "web-test", Sub: sub, Exp: time.Now().Add(time.Hour).Unix()}, "HS256", []byte("s"))
}

func srv(t *testing.T, d Doer) http.Handler {
	t.Helper()
	h, err := platform.New(platform.Config{Keys: keys}, New(Config{Doer: d}))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func do(h http.Handler, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token("s"))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func problemOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type %q, body %s", ct, rec.Body.String())
	}
	var p map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

const uid = "/api/v2/users/7f3a0000-0000-4000-8000-000000000001"

func TestModule_NameAndPrefixes(t *testing.T) {
	m := New(Config{Doer: noDB{t}})
	if m.Name() != "admin" || m.Prefixes()[0] != "/api/v2/users" {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
}

func TestUsers_RoutesAreThere(t *testing.T) {
	mux := http.NewServeMux()
	New(Config{Doer: noDB{t}}).Routes(mux)
	for _, want := range []string{
		"GET /api/v2/users", "POST /api/v2/users", "GET /api/v2/users/{id}", "PATCH /api/v2/users/{id}", "DELETE /api/v2/users/{id}",
		"PATCH /api/v2/users/{id}/profile", "POST /api/v2/users/{id}/actions/{action}",
		"GET /api/v2/users/{id}/iptable", "PUT /api/v2/users/{id}/iptable",
		"GET /api/v2/users/{id}/memberships",
		"GET /api/v2/users/{id}/groups", "POST /api/v2/users/{id}/groups", "DELETE /api/v2/users/{id}/groups/{gid}",
		"GET /api/v2/users/{id}/areas", "POST /api/v2/users/{id}/areas", "DELETE /api/v2/users/{id}/areas/{gid}",
		"GET /api/v2/users/{id}/interfaces", "POST /api/v2/users/{id}/interfaces", "DELETE /api/v2/users/{id}/interfaces/{gid}",
	} {
		method, pattern, _ := strings.Cut(want, " ")
		path := strings.NewReplacer("{id}", "7f3a0000-0000-4000-8000-000000000001", "{gid}", "7f3a0000-0000-4000-8000-000000000002", "{action}", "lock").Replace(pattern)
		r, _ := http.NewRequest(method, "http://x"+path, nil)
		if _, got := mux.Handler(r); got != want {
			t.Fatalf("%s → %q", want, got)
		}
	}
}

func TestUsers_UnknownKeyIs400_WithoutDB(t *testing.T) {
	rec := do(srv(t, noDB{t}), "POST", "/api/v2/users", `{"username":"a","nope":1}`)
	if p := problemOf(t, rec); rec.Code != 400 || !strings.Contains(p["detail"].(string), "nope") {
		t.Fatalf("%d %v", rec.Code, p)
	}
}

func TestUsers_CreateNeedsUsername_WithoutDB(t *testing.T) {
	rec := do(srv(t, noDB{t}), "POST", "/api/v2/users", `{"name":"a"}`)
	if rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestUsers_PatchWithoutIfMatchIs428_WithoutDB(t *testing.T) {
	for _, path := range []string{uid, uid + "/profile"} {
		rec := do(srv(t, noDB{t}), "PATCH", path, `{"name":"b"}`)
		if rec.Code != 428 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestUsers_UnknownActionIs404_WithoutDB(t *testing.T) {
	rec := do(srv(t, noDB{t}), "POST", uid+"/actions/explode", "")
	if p := problemOf(t, rec); rec.Code != 404 || p["type"] != "urn:apostol:error:not-found" {
		t.Fatalf("%d %v", rec.Code, p)
	}
}

func TestUsers_ChangePasswordNeedsBothPasswords_WithoutDB(t *testing.T) {
	rec := do(srv(t, noDB{t}), "POST", uid+"/actions/change-password", `{"old":"a"}`)
	if rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestUsers_MembershipPostNeedsID_WithoutDB(t *testing.T) {
	rec := do(srv(t, noDB{t}), "POST", uid+"/groups", `{}`)
	if rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := do(srv(t, noDB{t}), "DELETE", uid+"/groups/nope", ""); rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestUsers_IptablePutRefusesUnknownKey_WithoutDB(t *testing.T) {
	rec := do(srv(t, noDB{t}), "PUT", uid+"/iptable", `{"allow":["10.0.0.0/8"],"block":[]}`)
	if rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
