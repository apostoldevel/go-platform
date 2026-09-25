package problem

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFromCatalogCode_StatusIsGGG(t *testing.T) {
	cases := map[string]int{"ERR-400-032": 400, "ERR-409-004": 409, "ERR-412-001": 412, "ERR-429-001": 429, "ERR-401-001": 401, "ERR-500-001": 500}
	for code, want := range cases {
		p := FromCode(code, "title", "detail")
		if p.Status != want || p.Type != "urn:apostol:error:"+code || p.Code == nil || *p.Code != code {
			t.Errorf("%s: %+v", code, p)
		}
	}
}

func TestFromCatalogCode_NonHTTPGroupIs400(t *testing.T) {
	for _, code := range []string{"ERR-000-001", "ERR-099-001", "ERR-600-001"} {
		if p := FromCode(code, "t", "d"); p.Status != 400 {
			t.Errorf("%s: status %d", code, p.Status)
		}
	}
}

func TestParseMessage(t *testing.T) {
	code, text, ok := ParseMessage("ERR-400-032: Object [x] method not FOUND, for action: y")
	if !ok || code != "ERR-400-032" || text != "Object [x] method not FOUND, for action: y" {
		t.Fatalf("%q %q %v", code, text, ok)
	}
	if _, _, ok := ParseMessage("ERR-40000: bare"); ok {
		t.Fatal("bare five-digit code must not parse")
	}
	if _, _, ok := ParseMessage("plain text"); ok {
		t.Fatal("plain text must not parse")
	}
}

func TestWrite_ContentTypeStatusBody(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v2/clients/7f", nil)
	req.Header.Set("X-Request-Id", "6f1c")
	p := New(http.StatusUnauthorized, "unauthorized", "Unauthorized", "signature")
	p.Write(rec, req)
	if rec.Code != 401 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["type"] != "urn:apostol:error:unauthorized" || got["status"] != float64(401) || got["instance"] != "/api/v2/clients/7f" || got["request_id"] != "6f1c" || got["code"] != nil {
		t.Fatalf("%v", got)
	}
	if _, has := got["code"]; !has {
		t.Fatal("code must be present (null) for machine readers")
	}
}

func TestError_Unwrap(t *testing.T) {
	p := New(404, "not-found", "Not found", "")
	err := error(p)
	var got *Problem
	if !errors.As(err, &got) || got.Status != 404 {
		t.Fatal("Problem must be an error")
	}
}

func TestWithCode_KeepsSlugSetsCode(t *testing.T) {
	base := New(401, "unauthorized", "Login failed", "Bearer token required")
	p := base.WithCode("ERR-401-001")
	if p.Type != "urn:apostol:error:unauthorized" || p.Status != 401 || p.Code == nil || *p.Code != "ERR-401-001" || p.Title != "Login failed" {
		t.Fatalf("%+v", p)
	}
	if base.Code != nil {
		t.Fatal("WithCode must not change the receiver")
	}
}

type oneTitle struct{}

func (oneTitle) Title(code, fallback string) string {
	if code == "ERR-401-001" {
		return "Login failed"
	}
	return fallback
}

func TestUnauthorized_NilSafeTitledFromCatalogue(t *testing.T) {
	if p := Unauthorized(nil, "ERR-401-008", "jwt: expired"); p.Title != "Unauthorized" || p.Code == nil || *p.Code != "ERR-401-008" || p.Status != 401 || p.Type != "urn:apostol:error:unauthorized" {
		t.Fatalf("nil catalogue: %+v", p)
	}
	if p := Unauthorized(oneTitle{}, "ERR-401-001", "d"); p.Title != "Login failed" || p.Detail != "d" {
		t.Fatalf("%+v", p)
	}
	if p := Unauthorized(oneTitle{}, "ERR-401-007", "d"); p.Title != "Unauthorized" {
		t.Fatalf("code the catalogue lacks: %+v", p)
	}
}

// RFC 6750 §3: every 401 of a bearer-protected resource says how to present
// credentials. No credentials at all — the bare scheme, no error code
// (§3.1: SHOULD NOT); anything else — invalid_token, with the detail as the
// description only where §3 lets it stand (printable ASCII, no `"` or `\`).
func TestWrite_401CarriesBearerChallenge(t *testing.T) {
	for name, c := range map[string]struct {
		p    *Problem
		want string
	}{
		"not verified":   {Unauthorized(nil, "ERR-401-001", "The access token could not be verified."), `Bearer error="invalid_token", error_description="The access token could not be verified."`},
		"expired":        {Unauthorized(nil, "ERR-401-008", "The access token has expired."), `Bearer error="invalid_token", error_description="The access token has expired."`},
		"no credentials": {Unauthorized(nil, "ERR-401-001", "Bearer token required").NoCredentials(), `Bearer`},
		"catalogue 401":  {FromCode("ERR-401-009", "Доступ запрещён", "Вход с этого адреса запрещён"), `Bearer error="invalid_token"`},
		"quote":          {Unauthorized(nil, "ERR-401-001", `say "no"`), `Bearer error="invalid_token"`},
		"backslash":      {Unauthorized(nil, "ERR-401-001", `a\b`), `Bearer error="invalid_token"`},
		"line break":     {Unauthorized(nil, "ERR-401-001", "a\r\nX-Evil: 1"), `Bearer error="invalid_token"`},
		"no detail":      {Unauthorized(nil, "ERR-401-001", ""), `Bearer error="invalid_token"`},
	} {
		rec := httptest.NewRecorder()
		c.p.Write(rec, httptest.NewRequest("GET", "/api/v2/x", nil))
		if got := rec.Header().Values("WWW-Authenticate"); len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	for _, p := range []*Problem{New(403, "forbidden", "Forbidden", ""), New(404, "not-found", "Not found", ""), FromCode("ERR-400-032", "t", "d")} {
		rec := httptest.NewRecorder()
		p.Write(rec, httptest.NewRequest("GET", "/api/v2/x", nil))
		if got := rec.Header().Get("WWW-Authenticate"); got != "" {
			t.Errorf("%d: challenge on a non-401: %q", p.Status, got)
		}
	}
	// the challenge is not part of the body
	rec := httptest.NewRecorder()
	Unauthorized(nil, "ERR-401-001", "d").NoCredentials().Write(rec, httptest.NewRequest("GET", "/api/v2/x", nil))
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for k := range got {
		switch k {
		case "type", "title", "status", "detail", "instance", "code":
		default:
			t.Fatalf("body key %q: %v", k, got)
		}
	}
}

func TestNoCredentials_DoesNotChangeTheReceiver(t *testing.T) {
	base := Unauthorized(nil, "ERR-401-001", "d")
	_ = base.NoCredentials()
	rec := httptest.NewRecorder()
	base.Write(rec, httptest.NewRequest("GET", "/", nil))
	if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer error="invalid_token", error_description="d"` {
		t.Fatalf("%q", got)
	}
}
