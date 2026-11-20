package reference

import (
	"strings"
	"testing"

	et "github.com/apostoldevel/go-platform/internal/entitytest"
	"github.com/apostoldevel/go-platform/lib/openapi"
)

// The six routes of the form are described, and the note on who may write is on
// the three that write (POST, PATCH, the action) and on none that reads.
func TestOperations_GuardNote(t *testing.T) {
	ops := New(Config{Doer: et.NoDB{T: t}}).(openapi.Documented).Operations()
	if len(ops) != 6 {
		t.Fatalf("%d operations, want the six of the form", len(ops))
	}
	for _, o := range ops {
		noted := strings.Contains(o.Description, WritesNote)
		if noted != (o.Method != "GET") {
			t.Errorf("%s: note %v", o.Pattern(), noted)
		}
	}
}

func TestAdministratorWrites_DoesNotChangeItsInput(t *testing.T) {
	in := []openapi.Operation{{Method: "POST", Path: "/x", ID: "x_create", Description: "d"}}
	out := AdministratorWrites(in)
	if in[0].Description != "d" || !strings.HasSuffix(out[0].Description, WritesNote) {
		t.Fatalf("in %q out %q", in[0].Description, out[0].Description)
	}
}
