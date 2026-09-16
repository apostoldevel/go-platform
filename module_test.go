package platform_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/auth/jwt"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

var keys = jwt.Keyring{Audiences: map[string]jwt.Key{"web-test": {Alg: "HS256", Secret: []byte("s")}}, Issuers: []string{"accounts.test"}}

func token(sub string) string {
	return jwt.Sign(jwt.Claims{Iss: "accounts.test", Aud: "web-test", Sub: sub, Exp: time.Now().Add(time.Hour).Unix()}, "HS256", []byte("s"))
}

// fake is a module with a resource and an item route; seen records what the
// handler saw in the request context.
type fake struct {
	name string
	pref []string
	seen *pgtx.Session
}

func (f fake) Name() string       { return f.name }
func (f fake) Prefixes() []string { return f.pref }
func (f fake) Routes(mux *http.ServeMux) {
	for _, p := range f.pref {
		mux.HandleFunc("GET "+p, func(w http.ResponseWriter, r *http.Request) {
			if f.seen != nil {
				*f.seen = platform.SessionOf(r)
			}
			w.WriteHeader(200)
		})
		mux.HandleFunc("POST "+p, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) })
		mux.HandleFunc("GET "+p+"/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
		mux.HandleFunc("PATCH "+p+"/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
		mux.HandleFunc("DELETE "+p+"/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	}
}

func host(t *testing.T, cfg platform.Config, mods ...platform.Module) http.Handler {
	t.Helper()
	if cfg.Keys.Audiences == nil {
		cfg.Keys = keys
	}
	h, err := platform.New(cfg, mods...)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func do(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(""))
	for k, v := range hdr {
		req.Header.Set(k, v)
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

func TestPrefixes_UnionInRegistrationOrder(t *testing.T) {
	got := platform.Prefixes(fake{pref: []string{"/api/v2/b"}}, fake{pref: []string{"/api/v2/a", "/api/v2/c"}})
	if strings.Join(got, " ") != "/api/v2/b /api/v2/a /api/v2/c" {
		t.Fatalf("%v", got)
	}
}

func TestNew_DuplicatePrefixIsError(t *testing.T) {
	_, err := platform.New(platform.Config{Keys: keys}, fake{name: "a", pref: []string{"/api/v2/x"}}, fake{name: "b", pref: []string{"/api/v2/x"}})
	if err == nil || !strings.Contains(err.Error(), "/api/v2/x") || !strings.Contains(err.Error(), "b") {
		t.Fatalf("err = %v", err)
	}
}

func TestNew_NoModulesIsError(t *testing.T) {
	if _, err := platform.New(platform.Config{Keys: keys}); err == nil {
		t.Fatal("a host without modules would register nothing")
	}
}

func TestUnauthorized_401_BeforeAnyRoute(t *testing.T) {
	h := host(t, platform.Config{}, fake{name: "x", pref: []string{"/api/v2/x"}})
	for name, hdr := range map[string]map[string]string{
		"no header":     {},
		"not bearer":    {"Authorization": "Basic abc"},
		"bad signature": {"Authorization": "Bearer " + token("s") + "x"},
		"expired":       {"Authorization": "Bearer " + jwt.Sign(jwt.Claims{Iss: "accounts.test", Aud: "web-test", Sub: "s", Exp: time.Now().Add(-time.Minute).Unix()}, "HS256", []byte("s"))},
	} {
		hdr["X-Request-Id"] = "r1"
		rec := do(h, "GET", "/api/v2/x", hdr)
		if rec.Code != 401 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
		if p := problemOf(t, rec); p["type"] != "urn:apostol:error:unauthorized" || p["request_id"] != "r1" || rec.Header().Get("X-Request-Id") != "r1" {
			t.Fatalf("%s: %v", name, p)
		}
	}
}

func TestSessionOf_CarriesTokenSubjectAgentAndFirstForwardedFor(t *testing.T) {
	var seen pgtx.Session
	h := host(t, platform.Config{}, fake{name: "x", pref: []string{"/api/v2/x"}, seen: &seen})
	sub := strings.Repeat("a", 40)
	rec := do(h, "GET", "/api/v2/x", map[string]string{"Authorization": "Bearer " + token(sub), "User-Agent": "ua/1", "X-Forwarded-For": "10.0.0.1, 10.0.0.2"})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if seen.Code != sub || seen.Agent != "ua/1" || seen.Host != "10.0.0.1" {
		t.Fatalf("%+v", seen)
	}
}

func TestRequestID_IsEchoed(t *testing.T) {
	h := host(t, platform.Config{}, fake{name: "x", pref: []string{"/api/v2/x"}})
	rec := do(h, "GET", "/api/v2/x", map[string]string{"Authorization": "Bearer " + token("s"), "X-Request-Id": "rid-1"})
	if rec.Header().Get("X-Request-Id") != "rid-1" {
		t.Fatalf("%v", rec.Header())
	}
}

func TestUnknownRoute_404_ProblemJSON(t *testing.T) {
	h := host(t, platform.Config{}, fake{name: "x", pref: []string{"/api/v2/x"}})
	for _, path := range []string{"/api/v2/y", "/api/v2/x/", "/api/v2/x/1/2/3"} {
		rec := do(h, "GET", path, map[string]string{"Authorization": "Bearer " + token("s")})
		if rec.Code != 404 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
		if p := problemOf(t, rec); p["type"] != "urn:apostol:error:not-found" {
			t.Fatalf("%s: %v", path, p)
		}
	}
}

// ServeMux answers a non-canonical path with its own text/html redirect;
// the gateway forwards the client's path verbatim (K7), so that answer would
// reach the client. Every answer of the process is problem+json.
func TestNonCanonicalPath_404_NotARedirect(t *testing.T) {
	h := host(t, platform.Config{}, fake{name: "x", pref: []string{"/api/v2/x"}})
	for _, path := range []string{"/api/v2//x", "/api/v2/./x", "/api/v2/y/../x", "/api/v2/x/1/../2"} {
		rec := do(h, "GET", path, map[string]string{"Authorization": "Bearer " + token("s")})
		if rec.Code != 404 || rec.Header().Get("Location") != "" {
			t.Fatalf("%s: %d Location=%q %s", path, rec.Code, rec.Header().Get("Location"), rec.Body)
		}
		if p := problemOf(t, rec); p["type"] != "urn:apostol:error:not-found" {
			t.Fatalf("%s: %v", path, p)
		}
	}
}

func TestMethodNotAllowed_405_CarriesAllow(t *testing.T) {
	h := host(t, platform.Config{}, fake{name: "x", pref: []string{"/api/v2/x"}})
	auth := map[string]string{"Authorization": "Bearer " + token("s")}
	for path, allow := range map[string]string{"/api/v2/x": "GET, HEAD, POST", "/api/v2/x/1": "GET, HEAD, PATCH, DELETE"} {
		rec := do(h, "PUT", path, auth)
		if rec.Code != 405 || rec.Header().Get("Allow") != allow {
			t.Fatalf("%s: %d Allow=%q %s", path, rec.Code, rec.Header().Get("Allow"), rec.Body)
		}
		if p := problemOf(t, rec); p["type"] != "urn:apostol:error:method-not-allowed" {
			t.Fatalf("%s: %v", path, p)
		}
	}
}

func TestTwoModules_ShareOneMux(t *testing.T) {
	h := host(t, platform.Config{}, fake{name: "a", pref: []string{"/api/v2/a"}}, fake{name: "b", pref: []string{"/api/v2/b"}})
	auth := map[string]string{"Authorization": "Bearer " + token("s")}
	if rec := do(h, "POST", "/api/v2/a", auth); rec.Code != 201 {
		t.Fatalf("a: %d", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/v2/b/7", auth); rec.Code != 204 {
		t.Fatalf("b: %d", rec.Code)
	}
}

func TestInFlight_IsCountedAroundTheRequest(t *testing.T) {
	var n atomic.Int32
	var peak int32
	m := fake{name: "x", pref: []string{"/api/v2/x"}}
	h := host(t, platform.Config{InFlight: &n}, probe{fake: m, on: func() { peak = n.Load() }})
	do(h, "GET", "/api/v2/x", map[string]string{"Authorization": "Bearer " + token("s")})
	if peak != 1 || n.Load() != 0 {
		t.Fatalf("peak %d, after %d", peak, n.Load())
	}
}

// probe wraps fake so a route can observe state mid-request.
type probe struct {
	fake
	on func()
}

func (p probe) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+p.pref[0], func(w http.ResponseWriter, r *http.Request) { p.on(); w.WriteHeader(200) })
}
