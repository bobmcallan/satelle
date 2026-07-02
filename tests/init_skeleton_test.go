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
// authored dir (incl. stories), the embedded skill the baseline references, and the
// embedded operating PRINCIPLES materialised on disk (sty_94da9ac9 — the runtime
// index no longer overlays embedded docs, so they must live in .satelle). A second
// run is idempotent.
func TestInitSkeleton(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	for _, rel := range []string{
		".satelle/satelle.toml",
		".satelle/agents.toml",
		".satelle/documents/README.md",
		".satelle/workflows/README.md",
		".satelle/principles/README.md",
		".satelle/skills/README.md",
		".satelle/skills/satelle-step-summary.md",
		// Embedded operating principles are materialised so List-based SessionStart
		// injection + doc-list discovery find them on disk (sty_94da9ac9).
		".satelle/principles/satelle-agent-goals.md",
		".satelle/principles/satelle-agent-model.md",
	} {
		if _, err := os.Stat(filepath.Join(repo, rel)); err != nil {
			t.Errorf("init did not create %s: %v", rel, err)
		}
	}
	// The baseline workflow is embedded-only — init must not write it as a repo
	// file, so a repo's own workflow can take precedence (sty_3f9a6124).
	if _, err := os.Stat(filepath.Join(repo, ".satelle/workflows/satelle-baseline-workflow.md")); err == nil {
		t.Error("init must not scaffold the baseline workflow as a repo file")
	}
	// The removed .satelle/stories mirror must NOT be recreated (sty_746a0c98).
	if _, err := os.Stat(filepath.Join(repo, ".satelle/stories")); err == nil {
		t.Error("init must not scaffold .satelle/stories")
	}

	// The scaffold agents.toml documents the reviewer-model knob (sty_dad271fd).
	agents, err := os.ReadFile(filepath.Join(repo, ".satelle", "agents.toml"))
	if err != nil {
		t.Fatalf("read agents.toml: %v", err)
	}
	if !strings.Contains(string(agents), "model") || !strings.Contains(string(agents), "sonnet") {
		t.Errorf("scaffold agents.toml should document the reviewer model knob (sonnet):\n%s", agents)
	}

	// A second init is idempotent — it must report no new creations.
	out := mustRun(t, testBin, repo, "init")
	if strings.Contains(out, "  + ") {
		t.Errorf("second init created something (not idempotent):\n%s", out)
	}
}

// TestInitReconcilesStaleHooks proves `satelle init` on a repo whose
// .claude/settings.json still invokes a RETIRED command rewrites it
// (sty_6a919dff): satelle index -> satelle reindex, other content preserved,
// idempotent.
func TestInitReconcilesStaleHooks(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	p := filepath.Join(repo, ".claude", "settings.json")
	stale := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"satelle index"},{"type":"command","command":"my-hook"}]}]}}`
	writeFile(t, p, stale)

	out := mustRun(t, testBin, repo, "init")
	if !strings.Contains(out, "hook updated") || !strings.Contains(out, "satelle index -> satelle reindex") {
		t.Errorf("init should report the hook reconciliation:\n%s", out)
	}
	got, _ := os.ReadFile(p)
	if !strings.Contains(string(got), `"satelle reindex"`) || strings.Contains(string(got), `"satelle index"`) {
		t.Errorf("stale hook not rewritten:\n%s", got)
	}
	if !strings.Contains(string(got), `"my-hook"`) {
		t.Errorf("user hook not preserved:\n%s", got)
	}
	// Idempotent: a third init reports present, no further update.
	if out3 := mustRun(t, testBin, repo, "init"); strings.Contains(out3, "hook updated") {
		t.Errorf("reconciliation should be idempotent:\n%s", out3)
	}
}
