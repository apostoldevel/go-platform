package notification

import (
	"net/http"
	"testing"

	"github.com/apostoldevel/go-platform/lib/openapi"
)

// What the module describes is what it mounts, and nothing more is mounted on
// the paths it describes.
func TestOperations_AreMounted(t *testing.T) {
	m := New(Config{})
	mux := http.NewServeMux()
	m.Routes(mux)
	ops := m.(openapi.Documented).Operations()
	if len(ops) == 0 {
		t.Fatal("nothing is described")
	}
	if missing, undocumented := openapi.Gaps(mux, ops); len(missing)+len(undocumented) != 0 {
		t.Fatalf("missing %v, undocumented %v", missing, undocumented)
	}
}
