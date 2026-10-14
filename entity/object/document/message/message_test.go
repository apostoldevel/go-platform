package message

import (
	"encoding/json"
	"testing"

	et "github.com/apostoldevel/go-platform/internal/entitytest"
)

func TestModule_NamePrefixRoutes(t *testing.T) {
	m := New(Config{Doer: et.NoDB{T: t}})
	if m.Name() != "message" || len(m.Prefixes()) != 1 || m.Prefixes()[0] != Prefix {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
	// the pure form: the six routes, none of the package's own (no DELETE —
	// no api.delete_message, no v1 delete flag)
	et.Routes(t, m, Prefix, "GET "+Prefix, "GET "+Prefix+"/{id}", "POST "+Prefix, "PATCH "+Prefix+"/{id}",
		"POST "+Prefix+"/{id}/actions/{action}", "GET "+Prefix+"/{id}/methods")
}

func TestTemplate_RefusedBeforeTheDatabase(t *testing.T) {
	et.Template(t, New(Config{Doer: et.NoDB{T: t}}), Prefix)
}

// What the create cannot do without is required, references are UUIDs,
// timestamps read; an explicit null keeps on PATCH.
func TestBody_Refusals(t *testing.T) {
	h := et.Srv(t, New(Config{Doer: et.NoDB{T: t}}))
	for _, b := range []string{
		`{"agent":"7f3a0000-0000-4000-8000-000000000002","code":"X","profile":"p","address":"a"}`,
		`{"type":"7f3a0000-0000-4000-8000-000000000002","code":"X","profile":"p","address":"a"}`,
		`{"type":"7f3a0000-0000-4000-8000-000000000002","agent":"7f3a0000-0000-4000-8000-000000000002","profile":"p","address":"a"}`,
		`{"type":"7f3a0000-0000-4000-8000-000000000002","agent":"7f3a0000-0000-4000-8000-000000000002","code":"X","address":"a"}`,
		`{"type":"7f3a0000-0000-4000-8000-000000000002","agent":"7f3a0000-0000-4000-8000-000000000002","code":"X","profile":"p"}`,
		`{"type":"7f3a0000-0000-4000-8000-000000000002","agent":"7f3a0000-0000-4000-8000-000000000002","code":"X","profile":"p","address":"a","parent":"abc"}`,
		`{"type":"abc","agent":"7f3a0000-0000-4000-8000-000000000002","code":"X","profile":"p","address":"a"}`,
		`{"type":"7f3a0000-0000-4000-8000-000000000002","agent":"abc","code":"X","profile":"p","address":"a"}`,
	} {
		p := et.ProblemOf(t, et.Do(h, "POST", Prefix, b), 400)
		t.Logf("%s: %v", b, p["detail"])
	}
	et.ProblemOf(t, et.Do(h, "PATCH", Prefix+"/"+et.ID, `{"code":""}`, "If-Match", `W/"1"`), 400)
	// subject and content are nullable in db.message: a message with neither is valid
	var ok body
	_ = json.Unmarshal([]byte(`{"type":"7f3a0000-0000-4000-8000-000000000002","agent":"7f3a0000-0000-4000-8000-000000000002","code":"X","profile":"p","address":"a"}`), &ok)
	if err := ok.Validate(true); err != nil {
		t.Fatalf("a message without subject and content: %v", err)
	}
	var b body
	_ = json.Unmarshal([]byte(`{"parent":null,"type":null,"agent":null,"code":null,"profile":null,"address":null,"subject":null,"content":null,"label":null,"description":null}`), &b)
	if err := b.Validate(false); err != nil {
		t.Fatalf("nulls on PATCH must keep: %v", err)
	}
}
