package platform_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
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

func TestSessionOf_CarriesTokenSubjectAgentAndClientAddress(t *testing.T) {
	var seen pgtx.Session
	h := host(t, platform.Config{}, fake{name: "x", pref: []string{"/api/v2/x"}, seen: &seen})
	sub := strings.Repeat("a", 40)
	rec := do(h, "GET", "/api/v2/x", map[string]string{"Authorization": "Bearer " + token(sub), "User-Agent": "ua/1", "X-Forwarded-For": "10.0.0.1, 10.0.0.2"})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// no trusted list: the peer (the gateway) is the only trusted proxy, the
	// client is what it appended — the last element, never the first
	if seen.Code != sub || seen.Agent != "ua/1" || seen.Host != "10.0.0.2" {
		t.Fatalf("%+v", seen)
	}
}

// The client's address, as nginx real_ip_recursive and Express trust proxy
// decide it: httptest's peer is 192.0.2.1.
func TestClientAddress_TrustedProxies(t *testing.T) {
	peerTrusted, _ := platform.ParseTrustedProxies("192.0.2.0/24, 10.0.0.0/8")
	none, _ := platform.ParseTrustedProxies("")
	for name, c := range map[string]struct {
		trusted []netip.Prefix
		xff     []string
		want    string
	}{
		"nil, no header: the peer":              {nil, nil, "192.0.2.1"},
		"nil: last element":                     {nil, []string{"1.2.3.4, 10.0.0.1, 10.0.0.2"}, "10.0.0.2"},
		"nil: two headers, last of the last":    {nil, []string{"1.2.3.4", "10.0.0.1, 10.0.0.2"}, "10.0.0.2"},
		"nil: a port and a mapped address":      {nil, []string{"[::ffff:203.0.113.9]:8080"}, "203.0.113.9"},
		"nil: v4 with a port":                   {nil, []string{"203.0.113.9:8080"}, "203.0.113.9"},
		"nil: junk last, the peer":              {nil, []string{"1.2.3.4, unknown"}, "192.0.2.1"},
		"trusted: rightmost untrusted":          {peerTrusted, []string{"1.2.3.4, 203.0.113.5, 10.0.0.1, 10.0.0.2"}, "203.0.113.5"},
		"trusted: the forged left part ignored": {peerTrusted, []string{"9.9.9.9, 203.0.113.5, 10.0.0.1"}, "203.0.113.5"},
		"trusted: all trusted, the leftmost":    {peerTrusted, []string{"10.0.0.1, 10.0.0.2"}, "10.0.0.1"},
		"trusted: no header, the peer":          {peerTrusted, nil, "192.0.2.1"},
		"trusted: junk stops at the last proxy": {peerTrusted, []string{"1.2.3.4, unknown, 10.0.0.1"}, "10.0.0.1"},
		"trusted: junk last, the peer":          {peerTrusted, []string{"1.2.3.4, unknown"}, "192.0.2.1"},
		"trusted: mapped CIDR trusts v4":        {mustPrefixes(t, "::ffff:192.0.2.0/120, 10.0.0.0/8"), []string{"203.0.113.5, 10.0.0.1"}, "203.0.113.5"},
		"trusted: bracketed IPv6 client":        {peerTrusted, []string{"[2001:db8::1], 10.0.0.1"}, "2001:db8::1"},
		"none: the peer, header ignored":        {none, []string{"1.2.3.4, 10.0.0.1"}, "192.0.2.1"},
		"peer not trusted: the peer":            {mustPrefixes(t, "10.0.0.0/8"), []string{"1.2.3.4, 10.0.0.1"}, "192.0.2.1"},
	} {
		t.Run(name, func(t *testing.T) {
			var seen pgtx.Session
			h := host(t, platform.Config{TrustedProxies: c.trusted}, fake{name: "x", pref: []string{"/api/v2/x"}, seen: &seen})
			req := httptest.NewRequest("GET", "/api/v2/x", strings.NewReader(""))
			req.Header.Set("Authorization", "Bearer "+token("s"))
			for _, v := range c.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if seen.Host != c.want {
				t.Fatalf("host %q, want %q", seen.Host, c.want)
			}
		})
	}
}

// The peer branch of addrOf with an IPv6 peer: httptest only gives a v4 one.
func TestClientAddress_IPv6Peer(t *testing.T) {
	var seen pgtx.Session
	h := host(t, platform.Config{TrustedProxies: mustPrefixes(t, "2001:db8::/32")}, fake{name: "x", pref: []string{"/api/v2/x"}, seen: &seen})
	req := httptest.NewRequest("GET", "/api/v2/x", strings.NewReader(""))
	req.RemoteAddr = "[2001:db8::7]:4321"
	req.Header.Set("Authorization", "Bearer "+token("s"))
	req.Header.Set("X-Forwarded-For", "203.0.113.5, [2001:db8::8]:1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || seen.Host != "203.0.113.5" {
		t.Fatalf("%d %q", rec.Code, seen.Host)
	}
	req.Header.Del("X-Forwarded-For")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen.Host != "2001:db8::7" {
		t.Fatalf("no header: %q", seen.Host)
	}
}

func mustPrefixes(t *testing.T, list string) []netip.Prefix {
	t.Helper()
	p, err := platform.ParseTrustedProxies(list)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseTrustedProxies(t *testing.T) {
	p, err := platform.ParseTrustedProxies(" 10.0.0.0/8 ,172.20.0.1, ::ffff:192.0.2.7 ")
	if err != nil || len(p) != 3 || p[0].String() != "10.0.0.0/8" || p[1].String() != "172.20.0.1/32" || p[2].String() != "192.0.2.7/32" {
		t.Fatalf("%v %v", p, err)
	}
	if p, err := platform.ParseTrustedProxies("::ffff:10.0.0.0/104"); err != nil || len(p) != 1 || p[0].String() != "10.0.0.0/8" {
		t.Fatalf("mapped CIDR must be unmapped: %v %v", p, err)
	}
	if p, err := platform.ParseTrustedProxies(""); err != nil || p == nil || len(p) != 0 {
		t.Fatalf("empty must be an empty list, not nil: %v %v", p, err)
	}
	if _, err := platform.ParseTrustedProxies("10.0.0.0/8, gateway"); err == nil {
		t.Fatal("a name is not an address")
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

// url.Values from r.URL.Query() drops a pair it cannot unescape and says
// nothing; a filter that disappears turns a selection into "everything"
// (seen in a live run). A query the host cannot parse is
// the client's error, before any route.
func TestMalformedQuery_400_BeforeAnyRoute(t *testing.T) {
	var seen pgtx.Session
	h := host(t, platform.Config{}, fake{name: "x", pref: []string{"/api/v2/x"}, seen: &seen})
	rec := do(h, "GET", "/api/v2/x?filter[username][like]=t303-probe-%&page[limit]=100", map[string]string{"Authorization": "Bearer " + token("s")})
	if rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if p := problemOf(t, rec); p["type"] != "urn:apostol:error:validation" || !strings.Contains(p["detail"].(string), "escape") {
		t.Fatal(p)
	}
	if seen.Code != "" {
		t.Fatal("the route was reached")
	}
	// a raw ";" is refused too (net/url since 1.17): before, the pair was
	// dropped just as silently — the client encodes it as %3B
	if rec := do(h, "GET", "/api/v2/x?filter[note][like]=a;b", map[string]string{"Authorization": "Bearer " + token("s")}); rec.Code != 400 {
		t.Fatalf("semicolon: %d %s", rec.Code, rec.Body)
	}
	// a well-formed query still passes, encoded or not
	for _, q := range []string{"?filter[username][like]=t303-probe-%25&page[limit]=100", "?filter%5Busername%5D%5Blike%5D=t303-probe-%25"} {
		if rec := do(h, "GET", "/api/v2/x"+q, map[string]string{"Authorization": "Bearer " + token("s")}); rec.Code != 200 {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}
}

// ServeMux answers a non-canonical path with its own text/html redirect;
// the gateway forwards the client's path verbatim, so that answer would
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

// A prefix under another one of the same process is not registered on its
// own: the gateway routes by longest prefix and refuses an overlap, and the
// process answers both — /api/v2/me covers /api/v2/me/event-log.
func TestPrefixes_ANestedPrefixIsCoveredByItsParent(t *testing.T) {
	got := platform.Prefixes(fake{pref: []string{"/api/v2/me/event-log"}}, fake{pref: []string{"/api/v2/me", "/api/v2/men"}})
	if strings.Join(got, " ") != "/api/v2/me /api/v2/men" {
		t.Fatalf("%v", got)
	}
}
