//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitHealsMissingDefaultVirtually: a repo with an authored workflow that
// references an embedded gate skill validates green without seeding the skill
// (sty_29e5a9a5 virtual resolution). Authored file is untouched.
func TestInitHealsMissingDefaultVirtually(t *testing.T) {
	repo := t.TempDir()
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	own := "---\nname: my-workflow\ntype: workflow\ndescription: my own lifecycle\napplies_to: [\"*\"]\nscope: project\n---\n\n# mine\n\n```dot\ndigraph w {\n  backlog -> in_progress -> done\n  estimate [agent=reviewer, prompt=\"@skill:satelle-estimate-actual-review\", on=\"in_progress,done\"]\n}\n```\n"
	if err := os.WriteFile(filepath.Join(wfDir, "my-workflow.md"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}

	mustRun(t, testBin, repo, "init")

	// Skill stays virtual — not on disk.
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "skills", "satelle-estimate-actual-review.md")); err == nil {
		t.Error("init must not seed the virtual gate skill")
	}
	// Authored workflow untouched.
	got, _ := os.ReadFile(filepath.Join(wfDir, "my-workflow.md"))
	if string(got) != own {
		t.Error("init modified the authored workflow")
	}
	// Idempotent.
	out := mustRun(t, testBin, repo, "init")
	if strings.Contains(out, "  + ") {
		t.Errorf("second init created something:\n%s", out)
	}
}
