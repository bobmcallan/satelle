//go:build integration

package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGateRetriesTransientReviewerFailure drives the real binary end-to-end: a
// reviewer subprocess that returns NO verdict on its first call — an empty/garbled
// result, as a rate-limited or killed nested agent does under concurrent satelle
// sessions (sty_d71b0791) — then a verdict, must NOT fail the transition on the
// first shot. The gate RETRIES the transient failure and the create succeeds,
// proving the retry is wired through the binary, not only the unit path.
func TestGateRetriesTransientReviewerFailure(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Opt into the content create-gate and author the rubric + a project workflow
	// (scoped to "feature") that declares it — the same wiring as the create-review
	// test, so the content reviewer (which runs through runReviewer, the retry path)
	// is exercised.
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"), "[review]\ngate_create = true\n")

	rubric, err := os.ReadFile(filepath.Join(repoRootForTest(), ".satelle", "skills", "satelle-story-create-review.md"))
	if err != nil {
		t.Fatalf("read rubric source: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".satelle", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "satelle-story-create-review.md"), string(rubric))

	wf, err := os.ReadFile(filepath.Join(repoRootForTest(), ".satelle", "workflows", "satelle-project-workflow.md"))
	if err != nil {
		t.Fatalf("read workflow source: %v", err)
	}
	wfBody := strings.Replace(string(wf), `applies_to: ["*"]`, `applies_to: ["feature"]`, 1)
	if err := os.MkdirAll(filepath.Join(repo, ".satelle", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "satelle-project-workflow.md"), wfBody)

	// A reviewer stub that returns NO verdict on call 1 (transient), a verdict after.
	// A counter file records the number of invocations so the retry is observable.
	counter := filepath.Join(repo, "reviewer-calls")
	verdict := filepath.Join(repo, "verdict.sh")
	script := "#!/bin/sh\n" +
		"c='" + counter + "'\n" +
		"n=$(cat \"$c\" 2>/dev/null || echo 0); n=$((n+1)); echo \"$n\" > \"$c\"\n" +
		"if [ \"$n\" -lt 2 ]; then echo 'transient: rate limited, no verdict'; exit 0; fi\n" +
		"echo '{\"decision\":\"accept\",\"notes\":\"\"}'\n"
	writeFile(t, verdict, script)
	_ = os.Chmod(verdict, 0o755)
	writeFile(t, filepath.Join(repo, ".satelle", "agents.toml"),
		fmt.Sprintf("[reviewer]\nharness = \"%s {system} {tools} {model}\"\n", verdict))

	mustRun(t, testBin, repo, "index")

	// The first reviewer call returns no verdict; the gate must retry and accept.
	if out, err := run(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "Add a widget", "--body", "Render a widget on the dashboard",
		"--acceptance", "1. the widget renders"); err != nil {
		t.Fatalf("gate should retry the transient no-verdict and create the story: %v\n%s", err, out)
	}
	if list := mustRun(t, testBin, repo, "story", "list"); !strings.Contains(list, "Add a widget") {
		t.Errorf("story should persist after a retried transient failure:\n%s", list)
	}
	// The stub must have been called at least twice — proving a retry occurred.
	if b, _ := os.ReadFile(counter); strings.TrimSpace(string(b)) == "" || strings.TrimSpace(string(b)) == "1" {
		t.Errorf("expected >= 2 reviewer calls (a retry), counter = %q", strings.TrimSpace(string(b)))
	}
	// The transient failure (the subprocess's own output) must be captured to
	// .satelle/logs/reviewer.log so cross-session API contention is reviewable.
	logb, _ := os.ReadFile(filepath.Join(repo, ".satelle", "logs", "reviewer.log"))
	if !strings.Contains(string(logb), "transient reviewer failure") || !strings.Contains(string(logb), "rate limited") {
		t.Errorf("transient failure + its output not captured to reviewer.log:\n%s", string(logb))
	}
}
