// Package platform is the root of go-platform: the Module contract every
// GoAPI package implements, and the host that composes modules into the one
// HTTP handler a process serves to the gateway.
//
// The Go tree mirrors the SQL tree of db-platform and of a project's
// configuration: one package per SQL module or entity, in the folder of its
// SQL path (workflow/, entity/object/document/job/, …). The shared library —
// gateway client, request transaction, problem+json, query translation, JWT —
// lives under lib/. A process is one binary per project: the list of imports
// in its main is the build's manifest, the way create.psql is for SQL.
package platform

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/apostoldevel/go-platform/lib/auth/jwt"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/problem"
)

// Module is one GoAPI package. Name is the SQL module or entity it mirrors,
// Prefixes go to the gateway's /register verbatim, Routes registers the
// package's handlers on the shared mux ("GET /api/v2/clients/{id}").
//
// Install/Migrate and the package's SQL are stage 2 of the design and are
// deliberately not here yet.
type Module interface {
	Name() string
	Prefixes() []string
	Routes(mux *http.ServeMux)
}

// Config is what the host needs beyond the modules: the JWT keys of the
// OAuth2 providers (the signature is verified here, before any route — the
// database trusts the session code), the clock for the verification, and an
// optional in-flight counter the gateway client reports.
type Config struct {
	Keys     jwt.Keyring
	Now      func() time.Time
	InFlight interface{ Add(int32) int32 }
}

type host struct {
	cfg Config
	mux *http.ServeMux
}

// New composes modules into one handler. Two modules claiming the same prefix
// is a manifest error, refused here rather than by the gateway at /register.
func New(cfg Config, modules ...Module) (http.Handler, error) {
	if len(modules) == 0 {
		return nil, fmt.Errorf("platform: no modules — the process would register nothing")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	owner := map[string]string{}
	h := &host{cfg: cfg, mux: http.NewServeMux()}
	for _, m := range modules {
		for _, p := range m.Prefixes() {
			if by, dup := owner[p]; dup {
				return nil, fmt.Errorf("platform: prefix %s claimed by both %s and %s", p, by, m.Name())
			}
			owner[p] = m.Name()
		}
		m.Routes(h.mux)
	}
	return h, nil
}

// Prefixes is the union of the modules' prefixes in registration order — the
// list the process sends to the gateway. A prefix nested under another one
// of the same process is left out: the gateway routes by longest prefix and
// refuses an overlap, and the parent already brings the request here.
func Prefixes(modules ...Module) []string {
	var all []string
	for _, m := range modules {
		all = append(all, m.Prefixes()...)
	}
	var out []string
	for _, p := range all {
		covered := false
		for _, q := range all {
			if q != p && strings.HasPrefix(p+"/", q+"/") {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	return out
}

type ctxKey int

const sessionKey ctxKey = 1

// SessionOf returns the session the host established from the bearer token:
// code = JWT sub, agent and host from the headers the gateway forwards (K7).
func SessionOf(r *http.Request) pgtx.Session {
	s, _ := r.Context().Value(sessionKey).(pgtx.Session)
	return s
}

// ServeHTTP: request id, in-flight accounting, JWT (contract K7), dispatch.
func (h *host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.cfg.InFlight != nil {
		h.cfg.InFlight.Add(1)
		defer h.cfg.InFlight.Add(-1)
	}
	if rid := r.Header.Get("X-Request-Id"); rid != "" {
		w.Header().Set("X-Request-Id", rid)
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tok == "" || tok == r.Header.Get("Authorization") {
		problem.New(401, "unauthorized", "Unauthorized", "Bearer token required").Write(w, r)
		return
	}
	claims, err := h.cfg.Keys.Verify(tok, h.cfg.Now())
	if err != nil {
		problem.New(401, "unauthorized", "Unauthorized", err.Error()).Write(w, r)
		return
	}
	sess := pgtx.Session{Code: claims.Sub, Agent: r.Header.Get("User-Agent"), Host: firstForwardedFor(r)}
	r = r.WithContext(context.WithValue(r.Context(), sessionKey, sess))
	// every answer of the process is problem+json: the mux's own 404/405 are
	// plain text, and a non-canonical path gets its text/html redirect — with a
	// non-empty pattern from Handler for every method, so it is decided first
	if !canonical(r.URL.Path) {
		problem.New(404, "not-found", "Not found", "").Write(w, r)
		return
	}
	if _, pattern := h.mux.Handler(r); pattern == "" {
		var allow []string
		for _, m := range []string{"GET", "POST", "PATCH", "DELETE"} {
			if _, p := h.mux.Handler(&http.Request{Method: m, URL: r.URL}); p != "" {
				allow = append(allow, m)
				if m == "GET" { // ServeMux serves HEAD wherever GET is registered
					allow = append(allow, "HEAD")
				}
			}
		}
		if len(allow) > 0 {
			w.Header().Set("Allow", strings.Join(allow, ", ")) // RFC 9110 §15.5.6
			problem.New(405, "method-not-allowed", "Method not allowed", "").Write(w, r)
			return
		}
		problem.New(404, "not-found", "Not found", "").Write(w, r)
		return
	}
	// r.URL.Query() drops a pair it cannot unescape and says nothing — a
	// filter that disappears turns a selection into "everything"; decided
	// here once, so no route ever sees a query the client did not send
	if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
		problem.New(400, "validation", "Bad request", "query: "+err.Error()).Write(w, r)
		return
	}
	h.mux.ServeHTTP(w, r)
}

// canonical mirrors ServeMux's cleanPath: what the mux would redirect, it
// treats as not found.
func canonical(p string) bool {
	if p == "" || p[0] != '/' {
		return false
	}
	c := path.Clean(p)
	if strings.HasSuffix(p, "/") && !strings.HasSuffix(c, "/") {
		c += "/"
	}
	return c == p
}

func firstForwardedFor(r *http.Request) string {
	xff := r.Header.Get("X-Forwarded-For")
	if i := strings.IndexByte(xff, ','); i >= 0 {
		xff = xff[:i]
	}
	return strings.TrimSpace(xff)
}
