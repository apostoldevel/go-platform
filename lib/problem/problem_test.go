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
