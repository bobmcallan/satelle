//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestBrokenRouteDiagnosisIsSurfaced (sty_88d40a60 AC3) reproduces the reported
// case end to end through the real binary: a repo whose done.toml carries an
// unknown key, the system story the indexer auto-raises for it, and then the two
// places that story must appear — a session start, and the refusal it explains.
//
// The reported failure was not that the diagnosis was missing. It existed twelve
// milliseconds after the breakage and sat unread in backlog for an hour while the
// symptom was hunted. So this test asserts the SURFACES, not the raise.
func TestBrokenRouteDiagnosisIsSurfaced(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerDispatch(t, repo)

	// Before: a sound repo says nothing extra — the bound that keeps this from
	// becoming noise (AC4), asserted on the real surface rather than a unit stub.
	if before := mustRun(t, testBin, repo, "hook", "context"); strings.Contains(before, "already diagnos") {
		t.Fatalf("a repo with no failing document must gain no advisory:\n%s", before)
	}

	// The exact reported fault: an unknown key under the wildcard lane's park.
	done := `[meta]
name = "done"
type = "workflow"
scope = "project"
description = "Fixture declaration of done carrying the reported unknown key."

["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked", agent = "reviewer" }
cancel = { state = "cancelled" }
`
	step := `[meta]
name = "step"
type = "workflow"
scope = "project"
description = "Fixture step catalogue."

[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(wfDir, "done.toml"), done)
	writeFile(t, filepath.Join(wfDir, "step.toml"), step)

	// The indexer files the diagnosis, naming the file and the key.
	out, _ := run(t, testBin, repo, "reindex")
	if !strings.Contains(out, "workflows/done") || !strings.Contains(out, "type:system") {
		t.Fatalf("reindex must file a system story for the malformed route source:\n%s", out)
	}
	if !strings.Contains(out, "unknown key") {
		t.Errorf("the filed story should name the fault:\n%s", out)
	}
	id := regexp.MustCompile(`sty_[0-9a-f]{8}`).FindString(out)
	if id == "" {
		t.Fatalf("no filed story id in reindex output:\n%s", id)
	}

	// AC1, on the reported case: a session start now names it.
	ctxOut := mustRun(t, testBin, repo, "hook", "context")
	for _, want := range []string{id, "workflows/done", "already diagnos"} {
		if !strings.Contains(ctxOut, want) {
			t.Errorf("session start must surface the diagnosis (%q missing):\n%s", want, ctxOut)
		}
	}

	// AC2, on the reported case: the refusal points at the same story instead of
	// leaving the operator to find it. The route source no longer parses, so the
	// transition fails closed — and now says where the answer already is.
	created, cerr := run(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "Work blocked by broken governance",
		"--body", "Any story is refused while the route source does not parse.",
		"--acceptance", "1. the refusal names the open diagnosis")
	if cerr != nil {
		t.Fatalf("create: %v\n%s", cerr, created)
	}
	storyID := regexp.MustCompile(`sty_[0-9a-f]{8}`).FindString(created)
	if storyID == "" {
		t.Fatalf("no story id in create output:\n%s", created)
	}
	refusal, rerr := run(t, testBin, repo, "story", "set", storyID, "--status", "in_progress")
	if rerr == nil {
		t.Fatalf("a broken route source must refuse the transition:\n%s", refusal)
	}
	if !strings.Contains(refusal, id) {
		t.Errorf("the refusal must name the open diagnosis %s:\n%s", id, refusal)
	}
	for _, want := range []string{"does not parse", "satelle story get " + id} {
		if !strings.Contains(refusal, want) {
			t.Errorf("refusal missing %q:\n%s", want, refusal)
		}
	}
}
