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

	// init also seeds the baseline WORKFLOW itself now (sty_bf153cbf); capture it
	// so we can prove restore leaves workflows alone (it only owns skills and
	// principles — 'satelle rebase' is the workflow reset path).
	baselineWF := filepath.Join(repo, ".satelle", "workflows", "satelle-baseline-workflow.md")
	wfBefore, err := os.ReadFile(baselineWF)
	if err != nil {
		t.Fatalf("expected init-seeded baseline workflow: %v", err)
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
	// restore never touches workflows — the seeded baseline workflow is untouched.
	if wfAfter, _ := os.ReadFile(baselineWF); string(wfAfter) != string(wfBefore) {
		t.Error("restore must not modify the seeded baseline workflow")
	}
}
