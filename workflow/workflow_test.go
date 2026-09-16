package workflow

import (
	"context"
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

const id = "7f3a0000-0000-4000-8000-000000000001"

func TestModule_NameAndPrefixes(t *testing.T) {
	m := New(Config{Doer: noDB{t}})
	want := "/api/v2/entities /api/v2/types /api/v2/classes /api/v2/states /api/v2/state-types /api/v2/actions /api/v2/methods /api/v2/transitions /api/v2/events /api/v2/event-types /api/v2/priorities"
	if m.Name() != "workflow" || strings.Join(m.Prefixes(), " ") != want {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
}

func TestRoutes(t *testing.T) {
	mux := http.NewServeMux()
	New(Config{Doer: noDB{t}}).Routes(mux)
	var want []string
	for _, ro := range []string{"entities", "actions", "priorities"} { // read-only catalogues
		want = append(want, "GET /api/v2/"+ro, "GET /api/v2/"+ro+"/{id}")
	}
	for _, rw := range []string{"types", "classes", "states", "methods", "transitions", "events"} {
		want = append(want, "GET /api/v2/"+rw, "GET /api/v2/"+rw+"/{id}", "POST /api/v2/"+rw, "PATCH /api/v2/"+rw+"/{id}", "DELETE /api/v2/"+rw+"/{id}")
	}
	want = append(want,
		"GET /api/v2/state-types", "GET /api/v2/state-types/{id}", "GET /api/v2/event-types", "GET /api/v2/event-types/{id}",
		"POST /api/v2/classes/{id}/actions/{action}",
		"GET /api/v2/classes/{id}/access", "PUT /api/v2/classes/{id}/access", "GET /api/v2/classes/{id}/access/decode",
		"GET /api/v2/methods/{id}/access", "PUT /api/v2/methods/{id}/access", "GET /api/v2/methods/{id}/access/decode",
	)
	rep := strings.NewReplacer("{id}", id, "{action}", "copy")
	for _, w := range want {
		method, pattern, _ := strings.Cut(w, " ")
		r, _ := http.NewRequest(method, "http://x"+rep.Replace(pattern), nil)
		if _, got := mux.Handler(r); got != w {
			t.Fatalf("%s → %q", w, got)
		}
	}
	// no writes on the catalogues
	for _, w := range []string{"POST /api/v2/entities", "PATCH /api/v2/actions/" + id, "DELETE /api/v2/priorities/" + id, "POST /api/v2/state-types"} {
		method, path, _ := strings.Cut(w, " ")
		r, _ := http.NewRequest(method, "http://x"+path, nil)
		if _, got := mux.Handler(r); got == w {
			t.Fatalf("%s must not exist", w)
		}
	}
}

func TestValidation_WithoutDB(t *testing.T) {
	h := srv(t, noDB{t})
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"type without code":        do(h, "POST", "/api/v2/types", `{"class":"`+id+`"}`),
		"class without code":       do(h, "POST", "/api/v2/classes", `{"parent":"`+id+`","entity":"`+id+`"}`),
		"state without class":      do(h, "POST", "/api/v2/states", `{"code":"x","type":"`+id+`"}`),
		"method without action":    do(h, "POST", "/api/v2/methods", `{"class":"`+id+`","state":"`+id+`"}`),
		"transition without new":   do(h, "POST", "/api/v2/transitions", `{"state":"`+id+`","method":"`+id+`"}`),
		"event without action":     do(h, "POST", "/api/v2/events", `{"class":"`+id+`","type":"`+id+`"}`),
		"unknown key":              do(h, "POST", "/api/v2/types", `{"code":"x","nope":1}`),
		"access set without mask":  do(h, "PUT", "/api/v2/classes/"+id+"/access", `{"userid":"`+id+`"}`),
		"access set bad userid":    do(h, "PUT", "/api/v2/methods/"+id+"/access", `{"userid":"nope","mask":7}`),
		"copy without destination": do(h, "POST", "/api/v2/classes/"+id+"/actions/copy", `{}`),
		"clone without code":       do(h, "POST", "/api/v2/classes/"+id+"/actions/clone", `{"entity":"`+id+`"}`),
	} {
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec := do(h, "POST", "/api/v2/classes/"+id+"/actions/explode", ""); rec.Code != 404 {
		t.Fatalf("unknown action: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "PATCH", "/api/v2/states/"+id, `{"label":"x"}`); rec.Code != 428 {
		t.Fatalf("patch without If-Match: %d", rec.Code)
	}
}
