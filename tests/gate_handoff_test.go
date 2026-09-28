//go:build integration

package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Black-box coverage for sty_c4b92c9e: a gate-running verb called by an AGENT
// returns before the harness background cutoff — no reviewer progress, a
// handle when the gate outlasts the wait — and the verdict is delivered into
// the session by the harness's own Stop hook, not fetched by the driver.
// Called from a terminal, nothing changes.

const (
	handoffGateSeconds = 6
	handoffWait        = "1s"
)

// slowReviewer makes the stub reviewer take `seconds` before accepting.
func slowReviewer(t *testing.T, repo string, seconds int) {
	t.Helper()
	verdict := filepath.Join(repo, "verdict.sh")
	writeFile(t, verdict, fmt.Sprintf(
		"#!/bin/sh\nsleep %d\necho '{\"decision\":\"accept\",\"notes\":\"stub accepted after a slow review\"}'\n", seconds))
	_ = os.Chmod(verdict, 0o755)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"),
		fmt.Sprintf("[reviewer]\ncommand = \"%s {system} {tools} {model}\"\n", verdict))
}

var agentGateEnv = []string{"SATELLE_GATE_MODE=agent", "SATELLE_GATE_WAIT=" + handoffWait}

// pendingHandle asserts out is the pending payload and returns its handle.
func pendingHandle(t *testing.T, out string) string {
	t.Helper()
	var p struct {
		Gate    string `json:"gate"`
		Handle  string `json:"handle"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &p); err != nil {
		t.Fatalf("the pending return is not a single JSON payload: %v\n%s", err, out)
	}
	if p.Gate != "pending" || !strings.HasPrefix(p.Handle, "gw_") {
		t.Fatalf("payload = %+v", p)
	}
	if !strings.Contains(p.Message, "do not poll") {
		t.Errorf("the pending line must forbid polling: %s", p.Message)
	}
	return p.Handle
}

// stopHook runs `satelle hook stopcheck` the way the harness's Stop hook does.
func stopHook(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command(testBin, "hook", "stopcheck")
	cmd.Dir = repo
	cmd.Env = append(isolatedEnv(t), "SATELLE_GATE_STOP_WAIT=60s")
	cmd.Stdin = strings.NewReader("{}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook stopcheck: %v\n%s", err, out)
	}
	return string(out)
}

// assertAgentFacingHandoff runs one gate-dispatching command against the slow
// reviewer and proves the whole contract: it returns inside the wait (not after
// the gate), prints no reviewer progress, hands back a handle — and the Stop
// hook then delivers the verdict, once.
func assertAgentFacingHandoff(t *testing.T, repo string, wantInVerdict string, args ...string) {
	t.Helper()
	start := time.Now()
	out, err := runEnv(t, testBin, repo, agentGateEnv, args...)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("satelle %s: a pending gate must exit clean: %v\n%s", strings.Join(args, " "), err, out)
	}
	if took >= handoffGateSeconds*time.Second {
		t.Fatalf("satelle %s took %s — it waited out the %ds gate instead of returning a handle", strings.Join(args, " "), took, handoffGateSeconds)
	}
	for _, progress := range []string{"running reviewer", "agent reviewer", "may take several minutes"} {
		if strings.Contains(out, progress) {
			t.Fatalf("reviewer progress reached the agent-facing stream (%q):\n%s", progress, out)
		}
	}
	handle := pendingHandle(t, out)

	// The wake: the Stop hook waits for the running gate and answers with the verdict.
	verdict := stopHook(t, repo)
	var blk struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(verdict)), &blk); err != nil {
		t.Fatalf("the Stop hook did not answer with a block: %v\n%s", err, verdict)
	}
	if blk.Decision != "block" || !strings.Contains(blk.Reason, handle) || !strings.Contains(blk.Reason, wantInVerdict) {
		t.Fatalf("Stop hook block = %+v, want the finished verdict for %s containing %q", blk, handle, wantInVerdict)
	}
	// The verdict and nothing else: not the record the command printed, and not
	// the step-self-report note a status change appends on stderr.
	for _, leaked := range []string{`"acceptance_criteria"`, `"created_at"`, "step-self-report"} {
		if strings.Contains(blk.Reason, leaked) {
			t.Fatalf("the delivered block carries %s — not part of the verdict:\n%s", leaked, blk.Reason)
		}
	}
	// Exactly once.
	if again := stopHook(t, repo); strings.Contains(again, handle) {
		t.Fatalf("the verdict was delivered twice:\n%s", again)
	}
}

// story create under gate_create, story set to a gated status, and story amend:
// three different verbs, one contract.
func TestAgentFacingGateVerbsReturnAHandleAndTheStopHookDeliversTheVerdict(t *testing.T) {
	t.Run("story create (gate_create)", func(t *testing.T) {
		repo := t.TempDir()
		mustRun(t, testBin, repo, "init")
		writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"), "[review]\ngate_create = true\n")
		stubAmendVerdict(t, repo)
		mustRun(t, testBin, repo, "reindex")
		slowReviewer(t, repo, handoffGateSeconds)
		assertAgentFacingHandoff(t, repo, "created: sty_",
			"story", "create", "--category", "feature", "--title", "Add a widget",
			"--body", "Render a widget on the dashboard", "--acceptance", "1. the widget renders")
	})

	t.Run("story set (gated transition)", func(t *testing.T) {
		repo := t.TempDir()
		mustRun(t, testBin, repo, "init")
		writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"),
			"[review]\ngate_create = false\n\n[categories]\nenforce = \"off\"\n")
		materializeDefault(t, repo, "skills", "satelle-story-intent-review")
		stubAmendVerdict(t, repo)
		writeSpineFixture(t, repo, "", "", "", "done|||satelle-story-intent-review|reviewer")
		mustRun(t, testBin, repo, "reindex")
		created := mustRun(t, testBin, repo, "story", "create", "--title", "Gate me",
			"--body", "Prove the hand-off on a transition", "--acceptance", "1. done", "--category", "handoff-test")
		id := storyIDFrom(t, created)
		slowReviewer(t, repo, handoffGateSeconds)
		assertAgentFacingHandoff(t, repo, "stub accepted after a slow review",
			"story", "set", id, "--status", "done")
		if got := mustRun(t, testBin, repo, "story", "get", id); !strings.Contains(got, `"status": "done"`) {
			t.Fatalf("the detached run must complete the transition it was handed:\n%s", got)
		}
	})

	t.Run("story amend", func(t *testing.T) {
		repo := t.TempDir()
		mustRun(t, testBin, repo, "init")
		stubAmendVerdict(t, repo)
		mustRun(t, testBin, repo, "reindex")
		id := engageForAmend(t, repo, "Add a widget")
		slowReviewer(t, repo, handoffGateSeconds)
		assertAgentFacingHandoff(t, repo, "accepted amendment of "+id,
			"story", "amend", id, "--acceptance", "1. the widget renders\n2. the widget is green",
			"--reason", "AC2 named the wrong colour")
		if got := mustRun(t, testBin, repo, "story", "get", id); !strings.Contains(got, "the widget is green") {
			t.Fatalf("the detached run must land the amendment it was handed:\n%s", got)
		}
	})
}

// A gate that finishes inside the wait returns its verdict inline, exactly as
// today — and there is then nothing left for a hook to deliver.
func TestAgentFacingFastGateReturnsTheVerdictInline(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubAmendVerdict(t, repo)
	mustRun(t, testBin, repo, "reindex")
	id := engageForAmend(t, repo, "Add a widget")

	out, err := runEnv(t, testBin, repo, []string{"SATELLE_GATE_MODE=agent", "SATELLE_GATE_WAIT=30s"},
		"story", "amend", id, "--acceptance", "1. the widget renders\n2. the widget is green", "--reason", "fix colour")
	if err != nil {
		t.Fatalf("an accepted amendment should land inline: %v\n%s", err, out)
	}
	if strings.Contains(out, `"gate":"pending"`) || !strings.Contains(out, "accepted amendment of "+id) {
		t.Fatalf("a fast gate must return the finished verdict block, not a handle:\n%s", out)
	}
	if strings.Contains(out, `"acceptance_criteria"`) {
		t.Fatalf("the inline return carries the story record, not just the verdict:\n%s", out)
	}
	if strings.Contains(stopHook(t, repo), "gw_") {
		t.Fatal("a verdict already returned inline was delivered again")
	}
}

// A rejected gate keeps its failure and its notes across the hand-off.
func TestAgentFacingRejectionKeepsItsNotesAndExitCode(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	setVerdict := stubAmendVerdict(t, repo)
	mustRun(t, testBin, repo, "reindex")
	id := engageForAmend(t, repo, "Add a widget")
	setVerdict("reject", "stub: this weakens AC2 rather than correcting it")

	out, err := runEnv(t, testBin, repo, []string{"SATELLE_GATE_MODE=agent", "SATELLE_GATE_WAIT=30s"},
		"story", "amend", id, "--acceptance", "1. the widget renders", "--reason", "drop the colour requirement")
	if err == nil {
		t.Fatalf("a rejected amendment must exit non-zero:\n%s", out)
	}
	if strings.Count(out, "weakens AC2") != 1 {
		t.Fatalf("the reject notes must reach the agent exactly once:\n%s", out)
	}
}

// AC4: from an interactive terminal the gate runs in the foreground, the
// reviewer's progress prints as it always did, and no detached run or handle is
// created.
func TestInteractiveCallerKeepsForegroundProgress(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubAmendVerdict(t, repo)
	mustRun(t, testBin, repo, "reindex")
	id := engageForAmend(t, repo, "Add a widget")
	// Setup ran with no terminal, so it went through the hand-off and left its own
	// (delivered) handles behind; the interactive call must add none.
	gates := filepath.Join(runtimeRoot(t, repo), "gates")
	before, _ := os.ReadDir(gates)

	out, err := runEnv(t, testBin, repo, []string{"SATELLE_GATE_MODE=interactive"},
		"story", "amend", id, "--acceptance", "1. the widget renders\n2. the widget is green", "--reason", "fix colour")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "running reviewer") {
		t.Fatalf("the interactive progress display changed — expected the reviewer progress line:\n%s", out)
	}
	if after, _ := os.ReadDir(gates); len(after) != len(before) {
		t.Fatalf("an interactive call created %d gate handle(s)", len(after)-len(before))
	}
}
