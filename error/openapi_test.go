package error

import (
	"net/http"
	"reflect"
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

// The three shapes of a body — the one the handler decodes, the one POST is
// described by and the one PATCH is — name the same keys.
func TestBodies_NameTheSameKeys(t *testing.T) {
	keys := func(v any) []string {
		var out []string
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			out = append(out, rt.Field(i).Tag.Get("json"))
		}
		return out
	}
	if a, b, c := keys(body{}), keys(Input{}), keys(PatchInput{}); !reflect.DeepEqual(a, b) || !reflect.DeepEqual(a, c) {
		t.Fatalf("body %v, input %v, patch %v", a, b, c)
	}
}
