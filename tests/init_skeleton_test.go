//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitSkeleton drives the real binary to prove `satelle init` lays a
// complete, self-documenting skeleton (sty_a2170bbf): the tomls, a README per
// authored dir (incl. stories), and the materialised baseline workflow + the
// embedded skill it references. A second run is idempotent.
func TestInitSkeleton(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	for _, rel := range []string{
		".satelle/satelle.toml",
		".satelle/actors.toml",
		".satelle/documents/README.md",
		".satelle/workflows/README.md",
		".satelle/principles/README.md",
		".satelle/skills/README.md",
		".satelle/stories/README.md",
		".satelle/workflows/satelle-baseline-workflow.md",
		".satelle/skills/satelle-step-summary.md",
	} {
		if _, err := os.Stat(filepath.Join(repo, rel)); err != nil {
			t.Errorf("init did not create %s: %v", rel, err)
		}
	}

	// The scaffold actors.toml documents the reviewer-model knob (sty_dad271fd).
	actors, err := os.ReadFile(filepath.Join(repo, ".satelle", "actors.toml"))
	if err != nil {
		t.Fatalf("read actors.toml: %v", err)
	}
	if !strings.Contains(string(actors), "model") || !strings.Contains(string(actors), "sonnet") {
		t.Errorf("scaffold actors.toml should document the reviewer model knob (sonnet):\n%s", actors)
	}

	// A second init is idempotent — it must report no new creations.
	out := mustRun(t, testBin, repo, "init")
	if strings.Contains(out, "  + ") {
		t.Errorf("second init created something (not idempotent):\n%s", out)
	}
}
