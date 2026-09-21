//go:build integration

package current

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/jackc/pgx/v5"
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

// withoutSessionTraces drops what any session of the same user moves between
// two reads: the input_* counters (every authorized request), state and
// statetext (another session's login or signout of this user — the
// packages of the suite run in parallel under one account) and lc_ip (the
// host of that user's last login). They belong to the user, not to the
// session, and are not what the parity is about. status/statustext and
// lock_date are the same class but flip only when the user's last session
// closes or on the first login after that — stable while the reader's own
// session lives, so they stay in.
func withoutSessionTraces(t *testing.T, row json.RawMessage) json.RawMessage {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(row, &m); err != nil {
		t.Fatal(err)
	}
	for k := range m {
		if strings.HasPrefix(k, "input_") || k == "state" || k == "statetext" || k == "lc_ip" {
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
			got, want = withoutSessionTraces(t, got), withoutSessionTraces(t, want)
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

// The suite's packages run in parallel under one account: another session
// of the same user logging in from another host and out again between the
// two reads of the parity moved state/statetext/lc_ip (1 of 11 runs, T172).
// Done here on purpose, the parity must hold.
func TestIntegration_MeParityUnderAnotherSessionOfTheUser(t *testing.T) {
	l := live(t)
	rec := l.Call("GET", "/api/v2/me", "")
	var parts map[string]json.RawMessage
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &parts) != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	admin := os.Getenv("GO_TEST_ADMIN_DSN")
	cfg, _ := pgx.ParseConfig(admin)
	conn, err := pgx.Connect(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var other string
	if err := conn.QueryRow(context.Background(), "SELECT session FROM api.login($1, $2, 'go-current-other', '10.255.255.254')", cfg.User, cfg.Password).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(context.Background(), "SELECT api.signout($1)", other); err != nil {
		t.Fatal(err)
	}
	got, want := parts["user"], l.Direct(t, "SELECT row_to_json(t) FROM api.current_user() t")
	if resttest.SameJSON(got, want) {
		// Login writes lc_ip unconditionally: a raw match means the race was
		// not reproduced, and the exclusion below would be proving nothing
		t.Fatal("the other session left no trace: the race was not reproduced")
	}
	if got, want = withoutSessionTraces(t, got), withoutSessionTraces(t, want); !resttest.SameJSON(got, want) {
		t.Fatalf("user:\n v2 %s\n sql %s", got, want)
	}
}
