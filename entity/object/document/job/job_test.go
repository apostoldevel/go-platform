package job

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

// The test harness the platform packages share in shape with
// configuration/csms/lib/entity/entitytest, which the module boundary
// forbids importing: the package under the platform host with a database
// that must not be reached.

// keys is the test audience the host verifies tokens against.
var keys = jwt.Keyring{Audiences: map[string]jwt.Key{"web-test": {Alg: "HS256", Secret: []byte("s")}}, Issuers: []string{"accounts.test"}}

// token signs a bearer of the test audience.
func token(sub string) string {
	return jwt.Sign(jwt.Claims{Iss: "accounts.test", Aud: "web-test", Sub: sub, Exp: time.Now().Add(time.Hour).Unix()}, "HS256", []byte("s"))
}

// noDB fails the test if a request reaches the database.
type noDB struct{ t *testing.T }

func (n noDB) Do(context.Context, pgtx.Session, *pgtx.Request, func(context.Context, pgx.Tx) error) error {
	n.t.Fatal("database reached")
	return nil
}

// srv mounts the module under the platform host, as cmd/platform-service
// runs it.
func srv(t *testing.T, m platform.Module) http.Handler {
	t.Helper()
	h, err := platform.New(platform.Config{Keys: keys}, m)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// do runs one authenticated request through the host.
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

// problemOf asserts a problem+json answer with the status and decodes it.
func problemOf(t *testing.T, rec *httptest.ResponseRecorder, status int) map[string]any {
	t.Helper()
	if rec.Code != status || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("%d %s: %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	var p map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// id is a well-formed id for the routes.
const id = "7f3a0000-0000-4000-8000-000000000001"

func TestModule_NamePrefixRoutes(t *testing.T) {
	m := New(Config{Doer: noDB{t}})
	if m.Name() != "job" || len(m.Prefixes()) != 1 || m.Prefixes()[0] != Prefix {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
	// the pure form: the six routes, none of the package's own (no DELETE —
	// no api.delete_job, no v1 delete flag)
	mux := http.NewServeMux()
	m.Routes(mux)
	for _, w := range []string{"GET " + Prefix, "GET " + Prefix + "/{id}", "POST " + Prefix, "PATCH " + Prefix + "/{id}",
		"POST " + Prefix + "/{id}/actions/{action}", "GET " + Prefix + "/{id}/methods"} {
		method, pattern := strings.Fields(w)[0], strings.Fields(w)[1]
		path := strings.NewReplacer("{id}", id, "{action}", "enable").Replace(pattern)
		r, _ := http.NewRequest(method, "http://x"+path, nil)
		if _, pat := mux.Handler(r); pat != w {
			t.Fatalf("%s → %q", w, pat)
		}
	}
}

// The pre-database refusals every package of the form shares: bad list
// query, bad id, 428, unknown body key, empty PATCH, bad action.
func TestTemplate_RefusedBeforeTheDatabase(t *testing.T) {
	h := srv(t, New(Config{Doer: noDB{t}}))
	for _, q := range []string{"?page[limit]=0", "?filter[name][between]=a", "?sort=name;drop", "?filter=x", "?limit=10", "?Q=x", "?reclimit=0", "?nosuch=1"} {
		problemOf(t, do(h, "GET", Prefix+q, ""), 400)
	}
	for _, path := range []string{Prefix + "/not-a-uuid", Prefix + "/not-a-uuid/methods"} {
		problemOf(t, do(h, "GET", path, ""), 400)
	}
	problemOf(t, do(h, "PATCH", Prefix+"/"+id, `{"code":"x"}`), 428)
	problemOf(t, do(h, "PATCH", Prefix+"/"+id, `{"no_such_key":"x"}`, "If-Match", `W/"1"`), 400)
	for _, b := range []string{``, `{}`, `{"code":""}`, `{"scheduler":"abc"}`} {
		problemOf(t, do(h, "PATCH", Prefix+"/"+id, b, "If-Match", `W/"1"`), 400)
	}
	for _, q := range []string{"?sort=sequence", "?page[limit]=1", "?filter[code]=x"} {
		problemOf(t, do(h, "GET", Prefix+"/"+id+"/methods"+q, ""), 400)
	}
	problemOf(t, do(h, "POST", Prefix, ``), 400)
	problemOf(t, do(h, "POST", Prefix+"/"+id+"/actions/Enable", ""), 400)
	problemOf(t, do(h, "POST", Prefix+"/"+id+"/actions/enable", "[1]"), 400)
	problemOf(t, do(h, "POST", Prefix+"/"+id+"/actions/enable", strings.Repeat(" ", 1<<20+1)+"{}"), 413)
}

// code, scheduler and program are required on create; references are UUIDs;
// daterun is a timestamp in one of the two accepted shapes.
func TestBody_Refusals(t *testing.T) {
	h := srv(t, New(Config{Doer: noDB{t}}))
	for _, b := range []string{`{"code":"J-1"}`, `{"code":"J-1","scheduler":"7f3a0000-0000-4000-8000-000000000001"}`,
		`{"code":"J-1","scheduler":"7f3a0000-0000-4000-8000-000000000001","program":"abc"}`,
		`{"code":"J-1","scheduler":"7f3a0000-0000-4000-8000-000000000001","program":"7f3a0000-0000-4000-8000-000000000002","daterun":"tomorrow"}`} {
		p := problemOf(t, do(h, "POST", Prefix, b), 400)
		t.Logf("%s: %v", b, p["detail"])
	}
	// both timestamp shapes are accepted: a valid body reaches the database,
	// which the NoDB doer fails the test on
	full := `{"code":"J-1","scheduler":"7f3a0000-0000-4000-8000-000000000001","program":"7f3a0000-0000-4000-8000-000000000002","daterun":"2030-01-02 03:04:05"}`
	var b body
	if err := json.Unmarshal([]byte(full), &b); err != nil {
		t.Fatal(err)
	}
	if err := b.Validate(true); err != nil {
		t.Fatalf("the space shape must be accepted: %v", err)
	}
	b.DateRun = ptr("2030-01-02T03:04:05Z")
	if err := b.Validate(true); err != nil {
		t.Fatalf("the RFC 3339 shape must be accepted: %v", err)
	}
	b.DateRun = ptr("2030-01-02T03:04:05+00:00")
	if err := b.Validate(true); err != nil {
		t.Fatalf("the RFC 3339 numeric-offset shape must be accepted: %v", err)
	}
}

// The body → api.set_job arguments: the id and every key, absent as nil —
// api.set_job reads a NULL as "keep", never a zero overwriting the column.
func TestBody_Args(t *testing.T) {
	var b body
	if err := json.Unmarshal([]byte(`{"code":"J-1","label":"Nightly","daterun":"2030-01-02 03:04:05"}`), &b); err != nil {
		t.Fatal(err)
	}
	args := b.Args("the-id")
	if len(args) != 9 || args["id"] != "the-id" || args["code"] != "J-1" || args["label"] != "Nightly" || args["daterun"] != "2030-01-02 03:04:05" {
		t.Fatalf("%v", args)
	}
	for _, k := range []string{"parent", "type", "scheduler", "program", "description"} {
		if v, has := args[k]; !has || v != nil {
			t.Fatalf("%s=%v present=%v", k, v, has)
		}
	}
	if err := b.Validate(true); err == nil {
		t.Fatal("a create without scheduler and program must be refused")
	}
}

func ptr(s string) *string { return &s }
