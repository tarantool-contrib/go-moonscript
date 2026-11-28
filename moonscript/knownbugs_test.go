package moonscript

import (
	"os"
	"testing"
)

// knownBug skips a test that fails because of a reported port bug. Run with
// MOONSCRIPT_KNOWN_BUGS=1 to execute these tests anyway (e.g. to check a
// fix): `MOONSCRIPT_KNOWN_BUGS=1 go test ./...`.
func knownBug(t *testing.T, reason string) {
	t.Helper()
	if os.Getenv("MOONSCRIPT_KNOWN_BUGS") == "" {
		t.Skip("known bug: " + reason)
	}
}
