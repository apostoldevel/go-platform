// Package problem is the module's error body (RFC 9457):
//
//	{"type":"urn:apostol:error:ERR-400-032","title":"…","status":400,"detail":"…",
//	 "instance":"/api/v2/clients/7f…","request_id":"<X-Request-Id>","code":"ERR-400-032"}
//
// Catalogue errors carry the ERR-GGG-CCC code in both `type` and `code`; the
// HTTP status is GGG whenever it is a valid HTTP status, else 400. Errors of
// the module itself use a slug: unauthorized, validation, not-found,
// precondition-failed, conflict, internal. A module-side problem that has a
// catalogue code keeps its slug in `type` (the class of the problem, stable
// for readers that branch on it) and carries the code in `code` — `code` is
// the machine key in every case, `null` only where the catalogue has none.
//
// Every 401 carries `WWW-Authenticate` (RFC 6750 §3): the bare `Bearer` when
// the request had no bearer at all (NoCredentials), `Bearer
// error="invalid_token"` for every other 401 — a token refused, a session the
// database does not know or refuses — with the detail as `error_description`
// where §3 lets it stand.
package problem

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
)

// Problem is one problem+json document. It is also an error.
type Problem struct {
	Type      string  `json:"type"`
	Title     string  `json:"title"`
	Status    int     `json:"status"`
	Detail    string  `json:"detail,omitempty"`
	Instance  string  `json:"instance,omitempty"`
	RequestID string  `json:"request_id,omitempty"`
	Code      *string `json:"code"` // ERR-GGG-CCC or null

	noCredentials bool // a 401 to a request with no bearer: challenge without an error code
}

// Error implements error.
func (p *Problem) Error() string {
	if p.Detail != "" {
		return p.Title + ": " + p.Detail
	}
	return p.Title
}

// Catalogue codes of the refusals the platform makes itself, before or
// around the database (db-platform error module).
const (
	CodeLoginFailed  = "ERR-401-001" // no credentials, a token that does not verify, a session the database does not know
	CodeTokenExpired = "ERR-401-008" // a verified token whose time is over
)

// Catalogue gives the catalogue message of a code, to title a problem the
// module makes itself; fallback when the catalogue has none (or no text
// usable as a title).
type Catalogue interface {
	Title(code, fallback string) string
}

// Unauthorized is every 401 the platform makes itself: slug type, the
// catalogue code, the catalogue's message as the title (the deployment's
// default locale — there is no session to take one from, the same as v1's
// LoginFailed before a session), "Unauthorized" when cat is nil or has none;
// detail is the technical reason.
func Unauthorized(cat Catalogue, code, detail string) *Problem {
	title := "Unauthorized"
	if cat != nil {
		title = cat.Title(code, title)
	}
	return New(http.StatusUnauthorized, "unauthorized", title, detail).WithCode(code)
}

// New builds a module-side problem with a slug type.
func New(status int, slug, title, detail string) *Problem {
	return &Problem{Type: "urn:apostol:error:" + slug, Title: title, Status: status, Detail: detail}
}

var codeRe = regexp.MustCompile(`^(ERR-(\d{3})-\d{3}): ?(.*)$`)

// ParseMessage splits a platform exception text `ERR-GGG-CCC: text` into its
// code and text — the same cut kernel.ParseMessage makes; anything else
// (including the misspelt `ERR-40000:` form) is not a catalogue error.
func ParseMessage(msg string) (code, text string, ok bool) {
	m := codeRe.FindStringSubmatch(msg)
	if m == nil {
		return "", "", false
	}
	return m[1], m[3], true
}

// WithCode returns a copy of the module-side problem carrying the catalogue
// code; the slug type is kept.
func (p *Problem) WithCode(code string) *Problem {
	out := *p
	out.Code = &code
	return &out
}

// NoCredentials returns a copy of the 401 marked as an answer to a request
// that presented no bearer at all: RFC 6750 §3.1 — the challenge then names
// the scheme and SHOULD NOT carry an error code.
func (p *Problem) NoCredentials() *Problem {
	out := *p
	out.noCredentials = true
	return &out
}

// challenge is the WWW-Authenticate value of a 401 (RFC 6750 §3).
func (p *Problem) challenge() string {
	if p.noCredentials {
		return "Bearer"
	}
	c := `Bearer error="invalid_token"`
	if describable(p.Detail) {
		c += `, error_description="` + p.Detail + `"`
	}
	return c
}

// describable: RFC 6750 §3 allows error_description only in %x20-21 /
// %x23-5B / %x5D-7E. A detail outside it (the catalogue's localized text, a
// quote, a line break) is left out rather than mangled — the body carries it.
func describable(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7e || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}

// FromCode builds a catalogue problem for ERR-GGG-CCC.
func FromCode(code, title, detail string) *Problem {
	status := 400
	if m := codeRe.FindStringSubmatch(code + ": "); m != nil {
		if g, _ := strconv.Atoi(m[2]); http.StatusText(g) != "" {
			status = g
		}
	}
	c := code
	return &Problem{Type: "urn:apostol:error:" + code, Title: title, Status: status, Detail: detail, Code: &c}
}

// Write sends the problem as the response, filling instance and request_id
// from the request (X-Request-Id is returned unchanged).
func (p *Problem) Write(w http.ResponseWriter, r *http.Request) {
	out := *p
	if out.Instance == "" && r != nil {
		out.Instance = r.URL.Path
	}
	if out.RequestID == "" && r != nil {
		out.RequestID = r.Header.Get("X-Request-Id")
	}
	w.Header().Set("Content-Type", "application/problem+json")
	if out.Status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", out.challenge())
	}
	if out.RequestID != "" {
		w.Header().Set("X-Request-Id", out.RequestID)
	}
	w.WriteHeader(out.Status)
	_ = json.NewEncoder(w).Encode(out)
}
