//go:build integration

package current

import (
	"encoding/json"
	"strings"
	"testing"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

func live(t *testing.T) *resttest.Live {
	return resttest.Start(t, "go-current-test", func(r *pgtx.Runner) platform.Module { return New(Config{Doer: r}) })
}

type me struct {
	User      map[string]any `json:"user"`
	Area      map[string]any `json:"area"`
	Interface map[string]any `json:"interface"`
	Locale    map[string]any `json:"locale"`
	OperDate  *string        `json:"oper_date"`
}

func getMe(t *testing.T, l *resttest.Live) me {
	t.Helper()
	rec := l.Call("GET", "/api/v2/me", "")
	var m me
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &m) != nil || m.User["id"] == nil || m.Area["id"] == nil || m.Locale["code"] == nil {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}
	return m
}

func withoutInputCounters(t *testing.T, row json.RawMessage) json.RawMessage {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(row, &m); err != nil {
		t.Fatal(err)
	}
	for k := range m {
		if strings.HasPrefix(k, "input_") {
			delete(m, k)
		}
	}
	out, _ := json.Marshal(m)
	return out
}

// GET /me is the seven v1 reads in one object, each part equal to its api.*.
func TestIntegration_MeIsTheSevenReads(t *testing.T) {
	l := live(t)
	rec := l.Call("GET", "/api/v2/me", "")
	var parts map[string]json.RawMessage
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &parts) != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for part, sql := range map[string]string{
		"user":      "SELECT row_to_json(t) FROM api.current_user() t",
		"area":      "SELECT row_to_json(t) FROM api.current_area() t",
		"interface": "SELECT row_to_json(t) FROM api.current_interface() t",
		"locale":    "SELECT row_to_json(t) FROM api.current_locale() t",
		"oper_date": "SELECT coalesce(to_json(api.oper_date()), 'null'::json)",
	} {
		got, want := parts[part], l.Direct(t, sql)
		if part == "user" {
			// every authorized request bumps the user's input_* counters: not part of the parity
			got, want = withoutInputCounters(t, got), withoutInputCounters(t, want)
		}
		if !resttest.SameJSON(got, want) {
			t.Fatalf("%s: %s", part, parts[part])
		}
	}
	if _, has := parts["session"]; has {
		t.Fatal("the session code is on the wire")
	}
}

// PATCH /me sets the session's locale (by code, then by id) and operating
// date (then clears it); each answer is the next GET. Restores what it touched.
func TestIntegration_PatchMeLocaleAndOperDate(t *testing.T) {
	l := live(t)
	before := getMe(t, l)
	orig, _ := before.Locale["code"].(string)
	t.Cleanup(func() { l.Call("PATCH", "/api/v2/me", `{"locale":"`+orig+`","oper_date":null}`) })
	other := "en"
	if orig == "en" {
		other = "ru"
	}
	rec := l.Call("PATCH", "/api/v2/me", `{"locale":"`+other+`"}`)
	var m me
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &m) != nil || m.Locale["code"] != other {
		t.Fatalf("by code: %d %s", rec.Code, rec.Body)
	}
	if again := getMe(t, l); again.Locale["code"] != other {
		t.Fatalf("not kept in the session: %v", again.Locale)
	}
	origID, _ := before.Locale["id"].(string)
	rec = l.Call("PATCH", "/api/v2/me", `{"locale":"`+origID+`"}`)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &m) != nil || m.Locale["code"] != orig {
		t.Fatalf("by id: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("PATCH", "/api/v2/me", `{"oper_date":"2026-01-02T03:04:05Z"}`)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &m) != nil || m.OperDate == nil || !resttest.SameJSON([]byte(`"`+*m.OperDate+`"`), l.Direct(t, "SELECT to_json(api.oper_date())")) {
		t.Fatalf("oper_date: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("PATCH", "/api/v2/me", `{"oper_date":null}`)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &m) != nil || m.OperDate != nil {
		t.Fatalf("oper_date null: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("PATCH", "/api/v2/me", `{"locale":"xx"}`); rec.Code != 400 {
		t.Fatalf("unknown locale: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("PATCH", "/api/v2/me", `{"area":"7f3a0000-0000-4000-8000-000000000001"}`); rec.Code != 404 && rec.Code != 400 {
		t.Fatalf("unknown area: %d %s", rec.Code, rec.Body)
	}
}
