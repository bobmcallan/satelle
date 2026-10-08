//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/testutil"
)

// Black-box coverage for sty_8f10499d: a gate that a session anchored in repo A
// starts against repo B is written into B's store, and A's own Stop hook — the
// harness fires it in the session's repo — still delivers its verdict, exactly
// once. The binary is the real one, both repos are really initialised, and the
// reviewer is the same slow stub the single-repo hand-off tests use.

const xrepoSession = "xrepo-dogfood-session"

// xrepoSessionEnv is the driving session's environment, with the dispatch markers
// blanked so that a suite run from inside a dispatched agent is still the driving
// session the Stop hook delivers to (a dispatched process never takes the wake).
// Pinned, the session's commands carry a project-dir pin naming repo A; unpinned
// they carry none, as when a harness does not hand its session's commands one.
func xrepoSessionEnv(repoA string, pinned bool) []string {
	pin := []string{"SATELLE_PROJECT_DIR=", "CLAUDE_PROJECT_DIR="}
	if pinned {
		pin = []string{"SATELLE_PROJECT_DIR=" + repoA}
	}
	return append(pin, "SATELLE_SESSION="+xrepoSession,
		"SATELLE_DISPATCH_AGENT=", "SATELLE_DISPATCH_STEP=", "SATELLE_DISPATCH_ITEM=", "SATELLE_DISPATCH_SPAWN=")
}

func TestGateStartedAcrossReposIsDeliveredByTheSessionsHooks(t *testing.T) {
	t.Run("pinned to the session's repo", func(t *testing.T) { gateAcrossRepos(t, true) })
	t.Run("pin published by the session's hooks", func(t *testing.T) { gateAcrossRepos(t, false) })
}

func gateAcrossRepos(t *testing.T, pinned bool) {
	repoA, repoB := t.TempDir(), t.TempDir()
	mustRun(t, testBin, repoA, "init")
	mustRun(t, testBin, repoB, "init")
	stubAmendVerdict(t, repoB)
	mustRun(t, testBin, repoB, "reindex")
	id := engageForAmend(t, repoB, "Add a widget")
	release := holdReviewer(t, repoB)
	if runtimeRoot(t, repoA) == runtimeRoot(t, repoB) {
		t.Fatalf("both repos share the runtime dir %s", runtimeRoot(t, repoA))
	}

	sessionEnv := xrepoSessionEnv(repoA, pinned)
	if !pinned {
		// Nothing in the command's environment names A: the session's own hook,
		// firing in A, is what tells a later command where it is served from.
		cmd := exec.Command(testBin, "hook", "stopcheck")
		cmd.Dir = repoA
		cmd.Env = append(isolatedEnv(t), sessionEnv...)
		cmd.Stdin = strings.NewReader("{}")
		if o, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook stopcheck in A: %v\n%s", err, o)
		}
	}

	// The session is served from A; the command acts on B.
	env := append(append([]string{}, agentGateEnv...), sessionEnv...)
	start := time.Now()
	out, err := runEnv(t, testBin, repoB, env, "story", "amend", id,
		"--acceptance", "1. the widget renders\n2. the widget is green", "--reason", "AC2 named the wrong colour")
	if err != nil {
		t.Fatalf("a pending gate must exit clean: %v\n%s", err, out)
	}
	// The gate is held until released, so a call that waited for it would still be
	// blocked here; returning inside the wait budget proves it handed off.
	if time.Since(start) >= testutil.WaitBudget {
		t.Fatalf("the call waited for the held gate instead of returning a handle (%s)", time.Since(start))
	}
	handle := pendingHandle(t, out)
	if _, err := os.Stat(filepath.Join(runtimeRoot(t, repoB), "gates", handle)); err != nil {
		t.Fatalf("the handle is not in B's store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runtimeRoot(t, repoA), "gates", handle)); err == nil {
		t.Fatalf("the handle is in A's store too")
	}

	// A's Stop hook, fired in A for the same session: it waits for the running gate
	// and answers with the verdict. The gate is let go first; the hook still waits
	// for the detached run to finish.
	release()
	stop := func() string {
		cmd := exec.Command(testBin, "hook", "stopcheck")
		cmd.Dir = repoA
		cmd.Env = append(append(isolatedEnv(t), sessionEnv...), "SATELLE_GATE_STOP_WAIT=60s")
		cmd.Stdin = strings.NewReader("{}")
		o, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hook stopcheck: %v\n%s", err, o)
		}
		return string(o)
	}
	var blk struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	verdict := stop()
	if err := json.Unmarshal([]byte(strings.TrimSpace(verdict)), &blk); err != nil {
		t.Fatalf("A's Stop hook did not answer with a block: %v\n%s", err, verdict)
	}
	if blk.Decision != "block" || !strings.Contains(blk.Reason, handle) || !strings.Contains(blk.Reason, "accepted amendment of "+id) {
		t.Fatalf("Stop hook block = %+v, want B's finished verdict for %s", blk, handle)
	}
	t.Logf("handle %s lives in %s; A's Stop hook delivered:\n%s", handle, filepath.Join(runtimeRoot(t, repoB), "gates"), blk.Reason)
	// Exactly once, from either repo's hooks.
	if again := stop(); strings.Contains(again, handle) {
		t.Fatalf("the verdict was delivered twice:\n%s", again)
	}
	cmd := exec.Command(testBin, "hook", "stopcheck")
	cmd.Dir = repoB
	cmd.Env = append(append(isolatedEnv(t), sessionEnv...), "SATELLE_GATE_STOP_WAIT=1s")
	cmd.Stdin = strings.NewReader("{}")
	if o, _ := cmd.CombinedOutput(); strings.Contains(string(o), handle) {
		t.Fatalf("B's hook delivered a verdict A had already delivered:\n%s", o)
	}
	// And the detached run really did its work in B.
	if got := mustRun(t, testBin, repoB, "story", "get", id); !strings.Contains(got, "the widget is green") {
		t.Fatalf("the detached run must land the amendment it was handed in B:\n%s", got)
	}
}
