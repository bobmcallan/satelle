//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitHealsMissingDefaultAdditively drives the real binary to prove init
// seeds the default solution ADDITIVELY and heals the satelle-server field
// deadlock (sty_f6bd6f84): a repo that authored its own wildcard project
// workflow but is missing the parent/task defaults AND a referenced gate skill
// is healed by re-running init — the missing defaults land, the authored file is
// untouched, and init validates green (exit zero) instead of refusing over the
// dangling reference.
func TestInitHealsMissingDefaultAdditively(t *testing.T) {
	repo := t.TempDir()
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Authored wildcard project workflow whose estimate gate references a gate
	// skill that is absent on disk — the dangling reference the old init refused.
	own := "---\nname: my-workflow\ntype: workflow\ndescription: my own lifecycle\napplies_to: [\"*\"]\nscope: project\n---\n\n# mine\n\n```dot\ndigraph w {\n  backlog -> in_progress -> done\n  estimate [agent=reviewer, prompt=\"@skill:satelle-estimate-actual-review\", on=\"in_progress,done\"]\n}\n```\n"
	if err := os.WriteFile(filepath.Join(wfDir, "my-workflow.md"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}

	// init must succeed (exit zero) — it heals rather than deadlocks.
	mustRun(t, testBin, repo, "init")

	// The missing non-overlapping defaults landed.
	for _, rel := range []string{
		".satelle/workflows/satelle-parent-workflow.md",
		".satelle/workflows/satelle-task-workflow.md",
		".satelle/skills/satelle-estimate-actual-review.md",
	} {
		if _, err := os.Stat(filepath.Join(repo, rel)); err != nil {
			t.Errorf("init did not heal %s: %v", rel, err)
		}
	}
	// The wildcard project default is NOT seeded beside the authored wildcard.
	if _, err := os.Stat(filepath.Join(wfDir, "satelle-project-workflow.md")); err == nil {
		t.Error("init seeded the wildcard project default beside an authored wildcard workflow")
	}
	// The authored workflow is untouched.
	got, _ := os.ReadFile(filepath.Join(wfDir, "my-workflow.md"))
	if string(got) != own {
		t.Error("init modified the authored workflow while healing")
	}

	// Idempotent: a second init creates nothing new.
	out := mustRun(t, testBin, repo, "init")
	if strings.Contains(out, "  + ") {
		t.Errorf("second init created something (not idempotent):\n%s", out)
	}
}
