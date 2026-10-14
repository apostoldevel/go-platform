package reference

import (
	"encoding/json"
	"testing"

	et "github.com/apostoldevel/go-platform/internal/entitytest"
)

func TestModule_NamePrefixRoutes(t *testing.T) {
	m := New(Config{Doer: et.NoDB{T: t}})
	if m.Name() != "reference" || len(m.Prefixes()) != 1 || m.Prefixes()[0] != Prefix {
		t.Fatalf("%s %v", m.Name(), m.Prefixes())
	}
	// the pure form: the six routes, none of the package's own (no DELETE —
	// no api.delete_reference, no v1 delete flag)
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
		`{"code":"X","name":"x"}`,
		`{"type":"7f3a0000-0000-4000-8000-000000000002","name":"x"}`,
		`{"type":"7f3a0000-0000-4000-8000-000000000002","code":"X","parent":"abc"}`,
		`{"type":"abc","code":"X"}`,
	} {
		p := et.ProblemOf(t, et.Do(h, "POST", Prefix, b), 400)
		t.Logf("%s: %v", b, p["detail"])
	}
	et.ProblemOf(t, et.Do(h, "PATCH", Prefix+"/"+et.ID, `{"code":""}`, "If-Match", `W/"1"`), 400)
	var b body
	_ = json.Unmarshal([]byte(`{"parent":null,"type":null,"code":null,"name":null,"description":null}`), &b)
	if err := b.Validate(false); err != nil {
		t.Fatalf("nulls on PATCH must keep: %v", err)
	}
}
