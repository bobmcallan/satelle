package verb_test

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
)

// withWiring restores verb's package-level wiring to what it was when the test
// started. Every test that calls verb.Set*, verb.Clear* or verb.Add* calls it
// first — never a hand-written reset to nil or zero, which would clobber a
// production default.
func withWiring(t *testing.T) {
	t.Helper()
	restore, _ := verb.SnapshotWiring()
	t.Cleanup(restore)
}
