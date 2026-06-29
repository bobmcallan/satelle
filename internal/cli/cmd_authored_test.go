package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/structure"
)

func TestValidArtifactName(t *testing.T) {
	ok := []string{"satelle-skill-review", "my-workflow", "x"}
	bad := []string{"", "a/b", "name.md", "dir\\name"}
	for _, n := range ok {
		if err := validArtifactName(n); err != nil {
			t.Errorf("validArtifactName(%q) = %v, want nil", n, err)
		}
	}
	for _, n := range bad {
		if err := validArtifactName(n); err == nil {
			t.Errorf("validArtifactName(%q) = nil, want error", n)
		}
	}
}

// The create command writes <kind-dir>/<name>.md only for kinds with a
// deterministic structure check, and its help names that check.
func TestAuthoredCreateWiredForStructuredKinds(t *testing.T) {
	for _, kind := range []string{"skills", "workflows", "principles"} {
		if !structure.Checked(kind) {
			t.Errorf("kind %q should have a deterministic structure check for gated create", kind)
		}
		c := authoredCreateCmd(kind)
		if !strings.Contains(c.Long, "structure check") {
			t.Errorf("create help for %q should mention its structure check", kind)
		}
	}
}
