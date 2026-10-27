// Package entitytest is the harness of the unit tests of the platform's
// entity packages: the package under the platform host with a database that
// must not be reached — what is refused before the transaction (a bad id, a
// missing If-Match, an unknown key, an unparsable query) is refused without
// it. It is the shape of the project harness over the same form (lib/entity);
// internal, so that it is not a second public API next to it.
package entitytest

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

// NoDB fails the test if a request reaches the database.
type NoDB struct{ T *testing.T }

// Do is rest.Doer.
func (n NoDB) Do(ctx context.Context, s pgtx.Session, req *pgtx.Request, fn func(context.Context, pgx.Tx) error) error {
	n.T.Fatal("database reached")
	return nil
}

// Keys is the test audience the host verifies tokens against.
var Keys = jwt.Keyring{Audiences: map[string]jwt.Key{"web-test": {Alg: "HS256", Secret: []byte("s")}}, Issuers: []string{"accounts.test"}}

// Token signs a bearer of the test audience.
func Token(sub string) string {
	return jwt.Sign(jwt.Claims{Iss: "accounts.test", Aud: "web-test", Sub: sub, Exp: time.Now().Add(time.Hour).Unix()}, "HS256", []byte("s"))
}

// Srv mounts the modules under the platform host, as cmd/platform-service
// runs them; the host's own behaviour (401 without the database, 404/405
// problem+json, Allow) is tested in go-platform, not here.
func Srv(t *testing.T, mods ...platform.Module) http.Handler {
	t.Helper()
	h, err := platform.New(platform.Config{Keys: Keys}, mods...)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// Do runs one authenticated request through the host.
func Do(h http.Handler, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+Token("s"))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ProblemOf asserts a problem+json answer with the status and decodes it.
func ProblemOf(t *testing.T, rec *httptest.ResponseRecorder, status int) map[string]any {
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

// ID is a well-formed id for the routes.
const ID = "7f3a0000-0000-4000-8000-000000000001"

// Routes asserts that the module registers exactly the patterns wanted
// (each as "METHOD /path/{id}…"). A route of the package's own beside the
// six of the form is asserted by the package's own test, not here.
func Routes(t *testing.T, m platform.Module, prefix string, want ...string) {
	t.Helper()
	mux := http.NewServeMux()
	m.Routes(mux)
	for _, w := range want {
		method, path := strings.Fields(w)[0], strings.Fields(w)[1]
		path = strings.NewReplacer("{id}", ID, "{action}", "enable", "{x}", ID).Replace(path)
		r, _ := http.NewRequest(method, "http://x"+path, nil)
		if _, pat := mux.Handler(r); pat != w {
			t.Fatalf("%s → %q", w, pat)
		}
	}
}

// Template runs the pre-database refusals every package of the form shares:
// bad list query, bad id, 428, unknown body key, empty PATCH, bad action.
func Template(t *testing.T, m platform.Module, prefix string) {
	t.Helper()
	h := Srv(t, m)
	for _, q := range []string{"?page[limit]=0", "?filter[name][between]=a", "?sort=name;drop", "?filter=x", "?limit=10", "?Q=x", "?reclimit=0"} {
		ProblemOf(t, Do(h, "GET", prefix+q, ""), 400)
	}
	for _, path := range []string{prefix + "/not-a-uuid", prefix + "/not-a-uuid/methods"} {
		ProblemOf(t, Do(h, "GET", path, ""), 400)
	}
	ProblemOf(t, Do(h, "PATCH", prefix+"/"+ID, `{"code":"x"}`), 428)
	ProblemOf(t, Do(h, "PATCH", prefix+"/"+ID, `{"no_such_key":"x"}`, "If-Match", `W/"1"`), 400)
	for _, b := range []string{``, `{}`, `{"code":""}`, `{"parent":"abc"}`} {
		ProblemOf(t, Do(h, "PATCH", prefix+"/"+ID, b, "If-Match", `W/"1"`), 400)
	}
	for _, q := range []string{"?sort=sequence", "?page[limit]=1", "?filter[name]=x"} {
		ProblemOf(t, Do(h, "GET", prefix+"/"+ID+"/methods"+q, ""), 400)
	}
	ProblemOf(t, Do(h, "POST", prefix, ``), 400)
	// the codes v1 gives the same refusals (IncorrectJsonKey, JsonIsEmpty): an
	// unknown key in a query or a body, an empty body; GET {id} takes fields
	// and nothing else, filter[…] and page[…] included
	for _, c := range []struct{ method, path, body, code string }{
		{"GET", prefix + "?limit=10", "", "ERR-400-061"},
		{"GET", prefix + "/" + ID + "?limit=10", "", "ERR-400-061"},
		{"GET", prefix + "/" + ID + "?filter[name]=x", "", "ERR-400-061"},
		{"GET", prefix + "/" + ID + "?fields=name&page[limit]=1", "", "ERR-400-061"},
		{"GET", prefix + "/" + ID + "/methods?sort=sequence", "", "ERR-400-061"},
		{"PATCH", prefix + "/" + ID, `{"no_such_key":"x"}`, "ERR-400-061"},
		{"POST", prefix, `{"no_such_key":"x"}`, "ERR-400-061"},
		{"POST", prefix, ``, "ERR-400-060"},
		{"PATCH", prefix + "/" + ID, ``, "ERR-400-060"},
		{"PATCH", prefix + "/" + ID, `{}`, ""},           // an empty object is not an empty body
		{"GET", prefix + "/" + ID + "?fields=A", "", ""}, // a name lib/query refuses: the module's own 400
	} {
		p := ProblemOf(t, Do(h, c.method, c.path, c.body, "If-Match", `W/"1"`), 400)
		if got, _ := p["code"].(string); got != c.code {
			t.Errorf("%s %s %q: code %q, want %q", c.method, c.path, c.body, got, c.code)
		}
	}
	ProblemOf(t, Do(h, "POST", prefix+"/"+ID+"/actions/Enable", ""), 400)
	ProblemOf(t, Do(h, "POST", prefix+"/"+ID+"/actions/enable", "[1]"), 400)
	ProblemOf(t, Do(h, "POST", prefix+"/"+ID+"/actions/enable", strings.Repeat(" ", 1<<20+1)+"{}"), 413)
}
