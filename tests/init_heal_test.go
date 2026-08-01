//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitHealsMissingDefaultVirtually: a repo with an authored route that
// references an embedded gate skill validates green without seeding the skill
// (sty_29e5a9a5 virtual resolution). Authored files are untouched.
func TestInitHealsMissingDefaultVirtually(t *testing.T) {
	repo := t.TempDir()
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	done, step := spineFixture("", "", "## gate satelle-estimate-actual-review\non: in_progress, done\nfor: *\n",
		"in_progress|executor|||",
		"done||||")
	for name, body := range map[string]string{"done.md": done, "step.md": step} {
		if err := os.WriteFile(filepath.Join(wfDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustRun(t, testBin, repo, "init")

	// Skill stays virtual — not on disk.
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "skills", "satelle-estimate-actual-review.md")); err == nil {
		t.Error("init must not seed the virtual gate skill")
	}
	// Authored route untouched.
	for name, body := range map[string]string{"done.md": done, "step.md": step} {
		got, _ := os.ReadFile(filepath.Join(wfDir, name))
		if string(got) != body {
			t.Errorf("init modified the authored %s", name)
		}
	}
	// Idempotent.
	out := mustRun(t, testBin, repo, "init")
	if strings.Contains(out, "  + ") {
		t.Errorf("second init created something:\n%s", out)
	}
}
