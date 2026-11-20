package reference

import (
	"strings"

	"github.com/apostoldevel/go-platform/lib/openapi"
)

// WritesNote is what the description of a write route of a reference says of
// who may call it: the route guard the platform registers for the prefix
// (GuardRead) lets any session read and leaves the writes to administrators.
const WritesNote = "Route guard: the gateway refuses this write to every session that is not an administrator (403), before the database; the routes that read are open to any session."

// AdministratorWrites adds WritesNote to the description of every operation of
// ops that is not a GET — the create, the update and the action of an entity
// whose prefix is guarded by GuardRead. The operations are copied; the
// slice given is not changed.
func AdministratorWrites(ops []openapi.Operation) []openapi.Operation {
	out := make([]openapi.Operation, len(ops))
	for i, o := range ops {
		if o.Method != "GET" {
			o.Description = strings.TrimSpace(o.Description + "\n\n" + WritesNote)
		}
		out[i] = o
	}
	return out
}
