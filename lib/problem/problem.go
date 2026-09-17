// Package problem is the module's error body (RFC 9457):
//
//	{"type":"urn:apostol:error:ERR-400-032","title":"…","status":400,"detail":"…",
//	 "instance":"/api/v2/clients/7f…","request_id":"<X-Request-Id>","code":"ERR-400-032"}
//
// Catalogue errors carry the ERR-GGG-CCC code in both `type` and `code`; the
// HTTP status is GGG whenever it is a valid HTTP status, else 400. Errors of
// the module itself use a slug: unauthorized, validation, not-found,
// precondition-failed, conflict, internal.
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
}

// Error implements error.
func (p *Problem) Error() string {
	if p.Detail != "" {
		return p.Title + ": " + p.Detail
	}
	return p.Title
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
	if out.RequestID != "" {
		w.Header().Set("X-Request-Id", out.RequestID)
	}
	w.WriteHeader(out.Status)
	_ = json.NewEncoder(w).Encode(out)
}
