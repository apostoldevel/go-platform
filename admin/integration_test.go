//go:build integration

package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/jackc/pgx/v5"
)

// Same environment as lib/pgtx: GO_TEST_PG_DSN (apibot, the pool),
// GO_TEST_ADMIN_DSN (mints the administrator's session, whose rights the
// admin module needs).
func live(t *testing.T) (http.Handler, *pgtx.Runner, pgtx.Session) {
	t.Helper()
	dsn, admin := os.Getenv("GO_TEST_PG_DSN"), os.Getenv("GO_TEST_ADMIN_DSN")
	if dsn == "" || admin == "" {
		t.Skip("GO_TEST_PG_DSN / GO_TEST_ADMIN_DSN not set")
	}
	pool, err := pgtx.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	cfg, _ := pgx.ParseConfig(admin)
	conn, err := pgx.Connect(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	var session string
	if err := conn.QueryRow(context.Background(), "SELECT session FROM api.login($1, $2, 'go-admin-test', '127.0.0.1')", cfg.User, cfg.Password).Scan(&session); err != nil {
		t.Fatal(err)
	}
	conn.Close(context.Background())
	t.Cleanup(func() {
		if c, err := pgx.Connect(context.Background(), admin); err == nil {
			_, _ = c.Exec(context.Background(), "SELECT api.signout($1)", session)
			c.Close(context.Background())
		}
	})
	runner := &pgtx.Runner{Pool: pool, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))}
	if err := runner.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	h, err := platform.New(platform.Config{Keys: keys}, New(Config{Doer: runner}))
	if err != nil {
		t.Fatal(err)
	}
	return h, runner, pgtx.Session{Code: session, Agent: "go-admin-test", Host: "127.0.0.1"}
}

func call(h http.Handler, sess pgtx.Session, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token(sess.Code))
	req.Header.Set("X-Request-Id", "it-admin-"+method)
	req.Header.Set("User-Agent", sess.Agent)
	req.Header.Set("X-Forwarded-For", sess.Host)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// direct runs api.* through the same request transaction the module uses —
// the v1 answer without the v1 envelope, for parity.
func direct(t *testing.T, runner *pgtx.Runner, sess pgtx.Session, sql string, args ...any) json.RawMessage {
	t.Helper()
	var row json.RawMessage
	if err := runner.Do(context.Background(), sess, &pgtx.Request{Method: "TEST", Path: "/parity"}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, sql, args...).Scan(&row)
	}); err != nil {
		t.Fatal(err)
	}
	return row
}

func TestIntegration_UsersLifecycleAndParity(t *testing.T) {
	h, runner, sess := live(t)
	name := fmt.Sprintf("go-test-%d", time.Now().UnixNano())

	// list = api.list_user + api.count_user
	rec := call(h, sess, "GET", "/api/v2/users?page[limit]=3&sort=-created&fields=id,username", "")
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
	_ = json.Unmarshal(direct(t, runner, sess, "SELECT api.count_user(NULL)"), &total)
	if total != list.Total {
		t.Fatalf("total %d, api.count_user %d", list.Total, total)
	}

	// create
	rec = call(h, sess, "POST", "/api/v2/users", `{"username":"`+name+`","password":"Go-test-1!","name":"Go test","email":"`+name+`@example.test"}`, "Idempotency-Key", name)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if id != "" {
		t.Cleanup(func() { call(h, sess, "DELETE", "/api/v2/users/"+id, "") })
	}
	if id == "" || rec.Header().Get("Location") != "/api/v2/users/"+id || rec.Header().Get("ETag") == "" {
		t.Fatalf("create headers: %v id=%s", rec.Header(), id)
	}
	if again := call(h, sess, "POST", "/api/v2/users", `{"username":"`+name+`","password":"Go-test-1!","name":"Go test","email":"`+name+`@example.test"}`, "Idempotency-Key", name); again.Code != 201 || again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay: %d %v", again.Code, again.Header())
	}

	// get: parity with api.get_user, ETag, 304
	rec = call(h, sess, "GET", "/api/v2/users/"+id, "")
	if rec.Code != 200 {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	want := direct(t, runner, sess, "SELECT row_to_json(t) FROM api.get_user($1::uuid) t", id)
	if !sameJSON(rec.Body.Bytes(), want) {
		t.Fatalf("parity:\n v2 %s\n v1 %s", rec.Body.Bytes(), want)
	}
	etag := rec.Header().Get("ETag")
	if nm := call(h, sess, "GET", "/api/v2/users/"+id, "", "If-None-Match", etag); nm.Code != 304 {
		t.Fatalf("304: %d", nm.Code)
	}

	// patch: stale ETag → 412, right → 200 with the change
	if st := call(h, sess, "PATCH", "/api/v2/users/"+id, `{"name":"Renamed"}`, "If-Match", `W/"stale"`); st.Code != 412 {
		t.Fatalf("412: %d %s", st.Code, st.Body)
	}
	rec = call(h, sess, "PATCH", "/api/v2/users/"+id, `{"name":"Renamed"}`, "If-Match", etag)
	var patched map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if rec.Code != 200 || patched["name"] != "Renamed" || rec.Header().Get("ETag") == etag {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	etag = rec.Header().Get("ETag")

	// profile
	rec = call(h, sess, "PATCH", "/api/v2/users/"+id+"/profile", `{"given_name":"Go","family_name":"Test"}`, "If-Match", etag)
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if rec.Code != 200 || patched["given_name"] != "Go" || patched["family_name"] != "Test" {
		t.Fatalf("profile: %d %s", rec.Code, rec.Body)
	}

	// lock / unlock
	rec = call(h, sess, "POST", "/api/v2/users/"+id+"/actions/lock", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if rec.Code != 200 || !strings.Contains(fmt.Sprint(patched["statustext"]), "locked") {
		t.Fatalf("lock: %d %s", rec.Code, rec.Body)
	}
	rec = call(h, sess, "POST", "/api/v2/users/"+id+"/actions/unlock", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if rec.Code != 200 || strings.Contains(fmt.Sprint(patched["statustext"]), "locked") {
		t.Fatalf("unlock: %d %s", rec.Code, rec.Body)
	}

	// iptable round trip
	rec = call(h, sess, "PUT", "/api/v2/users/"+id+"/iptable", `{"allow":["10.0.0.1-10.0.0.5","192.168.1.1"],"deny":[]}`)
	if rec.Code != 200 {
		t.Fatalf("iptable put: %d %s", rec.Code, rec.Body)
	}
	rec = call(h, sess, "GET", "/api/v2/users/"+id+"/iptable", "")
	var ipt iptable
	_ = json.Unmarshal(rec.Body.Bytes(), &ipt)
	if rec.Code != 200 || len(ipt.Allow) != 2 || len(ipt.Deny) != 0 {
		t.Fatalf("iptable get: %d %s", rec.Code, rec.Body)
	}

	// memberships: groups add → listed → delete; areas/interfaces readable; parity with api.member_group
	var group struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(direct(t, runner, sess, "SELECT row_to_json(t) FROM api.list_group(NULL, NULL, 1) t"), &group)
	if group.ID == "" {
		t.Fatal("no group to test membership with")
	}
	if rec = call(h, sess, "POST", "/api/v2/users/"+id+"/groups", `{"id":"`+group.ID+`"}`); rec.Code != 204 {
		t.Fatalf("group add: %d %s", rec.Code, rec.Body)
	}
	rec = call(h, sess, "GET", "/api/v2/users/"+id+"/groups", "")
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
	_ = json.Unmarshal(direct(t, runner, sess, "SELECT coalesce(json_agg(row_to_json(t)), '[]') FROM api.member_group($1::uuid) t", id), &v1)
	if len(v1) != len(groups) {
		t.Fatalf("parity groups: v2 %d, api.member_group %d", len(groups), len(v1))
	}
	if rec = call(h, sess, "DELETE", "/api/v2/users/"+id+"/groups/"+group.ID, ""); rec.Code != 204 {
		t.Fatalf("group delete: %d %s", rec.Code, rec.Body)
	}
	for _, sub := range []string{"areas", "interfaces", "memberships"} {
		if rec = call(h, sess, "GET", "/api/v2/users/"+id+"/"+sub, ""); rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "[") {
			t.Fatalf("%s: %d %s", sub, rec.Code, rec.Body)
		}
	}

	// unknown action, foreign id
	if rec = call(h, sess, "POST", "/api/v2/users/"+id+"/actions/explode", ""); rec.Code != 404 {
		t.Fatalf("explode: %d", rec.Code)
	}
	if rec = call(h, sess, "GET", "/api/v2/users/00000000-0000-4000-b000-00000000dead", ""); rec.Code != 404 {
		t.Fatalf("foreign: %d %s", rec.Code, rec.Body)
	}

	// delete → 204 → 404
	if rec = call(h, sess, "DELETE", "/api/v2/users/"+id, ""); rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec = call(h, sess, "GET", "/api/v2/users/"+id, ""); rec.Code != 404 {
		t.Fatalf("after delete: %d %s", rec.Code, rec.Body)
	}
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return string(xa) == string(ya)
}
