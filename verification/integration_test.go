//go:build integration

package verification

import (
	"encoding/json"
	"strings"
	"testing"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

func live(t *testing.T) *resttest.Live {
	return resttest.Start(t, "go-verification-test", func(r *pgtx.Runner) platform.Module { return New(Config{Doer: r}) })
}

// A code is issued, read back by id and in the list with parity, spent once,
// refused the second time and when unknown. Confirming marks the caller's
// channel verified — the channel already verified is chosen when there is one.
func TestIntegration_CodeLifecycle(t *testing.T) {
	l := live(t)
	var flags struct {
		Email bool `json:"email_verified"`
		Phone bool `json:"phone_verified"`
	}
	_ = json.Unmarshal(l.Direct(t, "SELECT json_build_object('email_verified', email_verified, 'phone_verified', phone_verified) FROM api.current_user()"), &flags)
	channel := "email"
	if !flags.Email && flags.Phone {
		channel = "phone"
	}
	rec := l.Call("POST", "/api/v2/verification/codes", `{"type":"`+channel+`"}`, "Idempotency-Key", "go-verification-"+channel)
	if rec.Code != 201 {
		t.Fatalf("issue: %d %s", rec.Code, rec.Body)
	}
	var issued struct {
		ID   string  `json:"id"`
		Type string  `json:"type"`
		Code string  `json:"code"`
		Used *string `json:"used"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &issued)
	if issued.Type != channel || issued.Code == "" || issued.Used != nil || rec.Header().Get("Location") != "/api/v2/verification/codes/"+issued.ID {
		t.Fatalf("issued: %s %v", rec.Body, rec.Header())
	}
	if again := l.Call("POST", "/api/v2/verification/codes", `{"type":"`+channel+`"}`, "Idempotency-Key", "go-verification-"+channel); again.Header().Get("Idempotency-Replayed") != "true" || !strings.Contains(again.Body.String(), issued.ID) {
		t.Fatalf("replay: %d %v", again.Code, again.Header())
	}
	rec = l.Call("GET", "/api/v2/verification/codes/"+issued.ID, "")
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), l.Direct(t, "SELECT row_to_json(t) FROM api.get_verification_code($1::uuid) t", issued.ID)) {
		t.Fatalf("get parity: %d %s", rec.Code, rec.Body)
	}
	p := resttest.ListOf(t, l.Call("GET", "/api/v2/verification/codes?filter[code]="+issued.Code, ""), "my codes")
	if p.Total != 1 || p.Items[0]["id"] != issued.ID {
		t.Fatalf("list: %+v", p)
	}
	rec = l.Call("POST", "/api/v2/verification/codes/confirm", `{"type":"`+channel+`","code":"`+issued.Code+`"}`)
	if rec.Code != 200 || rec.Body.String() != `{"confirmed":true}` {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("POST", "/api/v2/verification/codes/confirm", `{"type":"`+channel+`","code":"`+issued.Code+`"}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "already been used") {
		t.Fatalf("spent code: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("POST", "/api/v2/verification/codes/confirm", `{"type":"`+channel+`","code":"no-such-code"}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("unknown code: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/verification/codes/"+issued.ID, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &issued)
	if issued.Used == nil {
		t.Fatalf("not marked used: %s", rec.Body)
	}
}
