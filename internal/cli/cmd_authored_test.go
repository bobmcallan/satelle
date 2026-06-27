package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/reviewer"
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

// The create command writes <kind-dir>/<name>.md only for reviewer-backed kinds.
func TestAuthoredCreateWiredForReviewerKinds(t *testing.T) {
	for _, kind := range []string{"skills", "workflows", "principles"} {
		if reviewer.StructureReviewerFor(kind) == "" {
			t.Errorf("kind %q should map to a structure reviewer for gated create", kind)
		}
		c := authoredCreateCmd(kind)
		if !strings.Contains(c.Long, reviewer.StructureReviewerFor(kind)) {
			t.Errorf("create help for %q should name its structure reviewer", kind)
		}
	}
}
