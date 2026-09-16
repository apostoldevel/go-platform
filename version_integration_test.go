//go:build integration

package platform_test

import (
	"os"
	"testing"

	platform "github.com/apostoldevel/go-platform"
)

// The integration run names the db-platform it ran against (the submodule's
// VERSION file); the library's record of it must match, or the record lies.
func TestIntegration_DBPlatformVersionMatchesTheDatabaseUnderTest(t *testing.T) {
	want := os.Getenv("GO_TEST_DB_PLATFORM_VERSION")
	if want == "" {
		t.Skip("GO_TEST_DB_PLATFORM_VERSION not set — pass $(cat db/sql/platform/VERSION)")
	}
	if platform.DBPlatform != want {
		t.Fatalf("platform.DBPlatform = %q, integration ran against db-platform %q — update the constant in the same commit", platform.DBPlatform, want)
	}
}
