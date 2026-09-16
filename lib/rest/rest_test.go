package rest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/rest"
	"github.com/jackc/pgx/v5"
)

// noDB fails the test if a handler reaches the database.
type noDB struct{ t *testing.T }

func (n noDB) Do(context.Context, pgtx.Session, *pgtx.Request, func(context.Context, pgx.Tx) error) error {
	n.t.Fatal("database reached")
	return nil
}

func TestETagOf_LastUpdateThenUdateThenHash(t *testing.T) {
	if got := rest.ETagOf([]byte(`{"id":1,"lastupdate":"2026-09-16T10:00:00","udate":"x"}`)); got != `W/"2026-09-16T10:00:00"` {
		t.Fatal(got)
	}
	if got := rest.ETagOf([]byte(`{"id":1,"udate":"2026-09-16T10:00:00"}`)); got != `W/"2026-09-16T10:00:00"` {
		t.Fatal(got)
	}
	// no update stamp (api.user has none): a digest of the row — stable for the same row, different for another
	a, b, c := rest.ETagOf([]byte(`{"id":1,"name":"a"}`)), rest.ETagOf([]byte(`{"id":1,"name":"a"}`)), rest.ETagOf([]byte(`{"id":1,"name":"b"}`))
	if a == "" || a != b || a == c || !strings.HasPrefix(a, `W/"`) {
		t.Fatalf("%s %s %s", a, b, c)
	}
	if rest.ETagOf(nil) != "" || rest.ETagOf([]byte(`not json`)) != "" {
		t.Fatal("garbage must not get a tag")
	}
}

func TestIDOf_RefusesNonUUID(t *testing.T) {
	mux := http.NewServeMux()
	var got string
	var err error
	mux.HandleFunc("GET /x/{id}", func(w http.ResponseWriter, r *http.Request) { got, err = rest.IDOf(r) })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/x/not-a-uuid", nil))
	if err == nil || got != "" {
		t.Fatalf("%q %v", got, err)
	}
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/x/7f3a0000-0000-4000-8000-000000000001", nil))
	if err != nil || got != "7f3a0000-0000-4000-8000-000000000001" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestReadBody_UnknownKeyIs400_EmptyIsOK(t *testing.T) {
	var into struct {
		Name *string `json:"name"`
	}
	raw, err := rest.ReadBody(httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"a","nope":1}`)), &into)
	if err == nil {
		t.Fatalf("unknown key accepted: %s", raw)
	}
	raw, err = rest.ReadBody(httptest.NewRequest("POST", "/", strings.NewReader("")), &into)
	if err != nil || len(raw) != 0 {
		t.Fatalf("%v %q", err, raw)
	}
}

func TestList_BadParamsIs400WithoutDB(t *testing.T) {
	res := rest.Resource{Prefix: "/api/v2/xs", GetFn: "api.get_x", ListFn: "api.list_x", CountFn: "api.count_x"}
	rec := httptest.NewRecorder()
	res.List(noDB{t}, nil).ServeHTTP(rec, httptest.NewRequest("GET", "/api/v2/xs?page[limit]=0", nil))
	if rec.Code != 400 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestGet_BadIDIs400WithoutDB(t *testing.T) {
	res := rest.Resource{Prefix: "/api/v2/xs", GetFn: "api.get_x"}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v2/xs/{id}", res.Get(noDB{t}, nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v2/xs/nope", nil))
	if rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestIntID_GetTakesBigint(t *testing.T) {
	res := rest.Resource{Prefix: "/api/v2/event-log", GetFn: "api.get_event_log", IntID: true}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v2/event-log/{id}", res.Get(noDB{t}, nil))
	for _, bad := range []string{"nope", "7f3a0000-0000-4000-8000-000000000001", "-1", "0"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v2/event-log/"+bad, nil))
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", bad, rec.Code, rec.Body)
		}
	}
	if id, err := rest.IntIDOf(bound("GET /api/v2/event-log/{id}", "GET", "/api/v2/event-log/42")); err != nil || id != 42 {
		t.Fatalf("%d %v", id, err)
	}
}

// bound returns the request as the handler of pattern sees it (path values set).
func bound(pattern, method, path string) *http.Request {
	var out *http.Request
	m := http.NewServeMux()
	m.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) { out = r })
	m.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))
	return out
}

func TestFail_NonProblemIs500(t *testing.T) {
	rec := httptest.NewRecorder()
	rest.Fail(rec, httptest.NewRequest("GET", "/", nil), nil, context.DeadlineExceeded)
	var p map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if rec.Code != 500 || p["type"] != "urn:apostol:error:internal" {
		t.Fatalf("%d %v", rec.Code, p)
	}
}

func TestProject_KeepsRequestedFieldsOnly(t *testing.T) {
	out := rest.Project([]byte(`{"id":1,"a":2,"b":3}`), []string{"id", "b", "zzz"})
	if string(out) != `{"b":3,"id":1}` {
		t.Fatal(string(out))
	}
	if string(rest.Project([]byte(`{"id":1}`), nil)) != `{"id":1}` {
		t.Fatal("no fields = whole row")
	}
}

func TestIfMatch_StarMatchesAnyRow(t *testing.T) {
	r := httptest.NewRequest("PATCH", "/", nil)
	r.Header.Set("If-Match", "*")
	if err := rest.IfMatch(r, []byte(`{"id":1}`)); err != nil {
		t.Fatal(err)
	}
	r.Header.Set("If-Match", `W/"other"`)
	if err := rest.IfMatch(r, []byte(`{"id":1}`)); err == nil {
		t.Fatal("stale tag accepted")
	}
}

// The key of a stored answer is (user, resource path, Idempotency-Key): the
// same key on two resources of one session is two answers, not a conflict.
func TestIdempotency_KeyIsPerResource(t *testing.T) {
	idem := rest.NewIdempotency(time.Hour)
	users := rest.IdemScope(httptest.NewRequest("POST", "/api/v2/users", nil), "u1")
	groups := rest.IdemScope(httptest.NewRequest("POST", "/api/v2/groups", nil), "u1")
	c := &rest.Capture{ResponseWriter: httptest.NewRecorder()}
	c.WriteHeader(201)
	idem.Store(users, "k", []byte(`{"a":1}`), c)
	if _, conflict := idem.Lookup(groups, "k", []byte(`{"b":2}`)); conflict {
		t.Fatal("conflict across resources")
	}
	if rec, conflict := idem.Lookup(users, "k", []byte(`{"a":1}`)); conflict || rec == nil {
		t.Fatal("replay lost")
	}
	if _, conflict := idem.Lookup(users, "k", []byte(`{"a":2}`)); !conflict {
		t.Fatal("different body under the same key must conflict")
	}
}
