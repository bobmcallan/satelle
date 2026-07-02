//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRestoreRecoversDriftedSubstrate drives `satelle restore` end-to-end
// (sty_9e2426b3): a drifted embedded-default skill is restored with --yes;
// without confirmation (a "no" on stdin) nothing is written.
func TestRestoreRecoversDriftedSubstrate(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// init materialises the baseline's gate skills — drift one.
	skill := filepath.Join(repo, ".satelle", "skills", "satelle-step-summary.md")
	orig, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("expected init-materialised skill: %v", err)
	}
	if err := os.WriteFile(skill, []byte("broken: drifted by hand"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Refusal writes nothing.
	cmd := exec.Command(testBin, "restore")
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader("no\n")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "aborted") {
		t.Errorf("refusal should abort:\n%s", out)
	}
	if b, _ := os.ReadFile(skill); string(b) != "broken: drifted by hand" {
		t.Error("nothing must be written without confirmation")
	}

	// --yes restores the canonical bytes.
	rout := mustRun(t, testBin, repo, "restore", "--yes")
	if !strings.Contains(rout, "re-materialised") {
		t.Errorf("restore should report what it wrote:\n%s", rout)
	}
	got, _ := os.ReadFile(skill)
	if string(got) == "broken: drifted by hand" || len(got) == 0 {
		t.Error("drifted skill was not restored")
	}
	if string(got) != string(orig) {
		// init writes the same embedded bytes, so restore must reproduce them.
		t.Error("restored skill does not match the embedded default init materialised")
	}
	// The baseline workflow stays embedded-only.
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "workflows", "satelle-baseline-workflow.md")); err == nil {
		t.Error("restore must not write the baseline workflow to disk")
	}
}
