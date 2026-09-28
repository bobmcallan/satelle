package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/gatehandle"
)

// The Stop hook never releases a session to idle while a gate it handed off is
// still running (sty_5f15f263): at the wait bound it blocks with a still-running
// note, so the next Stop waits again.

// stopWakeRepo is a governed temp repo with a tiny wait bound.
func stopWakeRepo(t *testing.T, wait string) *gatehandle.Store {
	t.Helper()
	_ = tempRepo(t)
	clearHarnessEnv(t)
	t.Setenv(stopGateWaitEnv, wait)
	return gateStoreForTest(t)
}

// runningGate starts a handle whose process is the test binary itself: alive,
// with the identity SetPID recorded for it.
func runningGate(t *testing.T, store *gatehandle.Store, story string) gatehandle.Meta {
	t.Helper()
	m, err := store.Create(gatehandle.Meta{Verb: gateTestVerb, Story: story, Argv: []string{"story", "set", story, "--status", "ready"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPID(m.ID, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	return m
}

// stopOnce runs the Stop hook once and returns its decoded block, ok=false when
// it did not block.
func stopOnce(t *testing.T, raw string) (stopBlockOut, bool) {
	t.Helper()
	var out strings.Builder
	if err := runHookStopcheck([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	var blk stopBlockOut
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &blk); err != nil || blk.Decision != "block" {
		return stopBlockOut{}, false
	}
	return blk, true
}

// AC1: a gate still running at the wait bound blocks the stop; it does not
// allow it.
func TestStopHookBlocksWhileGateStillRunning(t *testing.T) {
	store := stopWakeRepo(t, "50ms")
	m := runningGate(t, store, "sty_slow")

	start := time.Now()
	blk, ok := stopOnce(t, "")
	if !ok {
		t.Fatal("a gate still running at the wait bound was let through: the stop was allowed")
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Errorf("the hook did not wait out its bound (%s)", time.Since(start))
	}
	for _, want := range []string{m.ID, "sty_slow", "still running after", "do not poll"} {
		if !strings.Contains(blk.Reason, want) {
			t.Errorf("the still-running note lacks %q:\n%s", want, blk.Reason)
		}
	}
	if store.Delivered(m.ID) {
		t.Error("a still-running note was recorded as the delivery of a verdict")
	}
	if strings.Contains(blk.Reason, "\n") {
		t.Errorf("the still-running note is not one line:\n%s", blk.Reason)
	}
}

// AC2: the next Stop waits again, and a gate that spans two waits is delivered
// once. AC4: stop_hook_active — set by the harness on the Stop that follows a
// block — never suppresses that delivery.
func TestStopHookGateSpanningTwoWaits(t *testing.T) {
	store := stopWakeRepo(t, "80ms")
	m := runningGate(t, store, "sty_span")

	if _, ok := stopOnce(t, ""); !ok {
		t.Fatal("the first wait did not block on a running gate")
	}
	if _, ok := stopOnce(t, `{"stop_hook_active":true}`); !ok {
		t.Fatal("the second wait did not block: stop_hook_active released a running gate")
	}

	_ = os.WriteFile(store.VerdictPath(m.ID), []byte("accepted ready→plan by satelle-story-intent-review\n"), 0o644)
	_ = store.Finish(m.ID, gatehandle.Result{})
	blk, ok := stopOnce(t, `{"stop_hook_active":true}`)
	if !ok || !strings.Contains(blk.Reason, "accepted ready→plan") || !strings.Contains(blk.Reason, m.ID) {
		t.Fatalf("the verdict was not delivered on a stop_hook_active Stop: %+v ok=%v", blk, ok)
	}
	if !store.Delivered(m.ID) {
		t.Error("the delivered verdict was not marked delivered")
	}
	if blk, ok := stopOnce(t, `{"stop_hook_active":true}`); ok && strings.Contains(blk.Reason, m.ID) {
		t.Fatalf("the verdict was delivered twice:\n%s", blk.Reason)
	}
}

// A gate that finishes during a wait is delivered by that wait, not the next.
func TestStopHookGateFinishingDuringWaitIsDeliveredOnce(t *testing.T) {
	store := stopWakeRepo(t, "10s")
	m := runningGate(t, store, "sty_during")
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = os.WriteFile(store.VerdictPath(m.ID), []byte("accepted plan→in_progress\n"), 0o644)
		_ = store.Finish(m.ID, gatehandle.Result{})
	}()
	blk, ok := stopOnce(t, `{"stop_hook_active":true}`)
	if !ok || !strings.Contains(blk.Reason, "accepted plan→in_progress") || strings.Contains(blk.Reason, "still running") {
		t.Fatalf("a gate finishing during the wait was not delivered: %+v ok=%v", blk, ok)
	}
}

// Antigravity blocks a stop only on decision "continue" (sty_9e88b82f): a still
// running gate and a delivered verdict must each carry it, or agy would let the
// session go idle and lose the verdict. The decision is the only difference from
// the paths above.
func TestStopHookGateDeliveryAntigravityContinues(t *testing.T) {
	store := stopWakeRepo(t, "50ms")
	prev := hookHarnessFlag
	hookHarnessFlag = "antigravity"
	t.Cleanup(func() { hookHarnessFlag = prev })
	stopAgy := func() stopBlockOut {
		t.Helper()
		var out strings.Builder
		if err := runHookStopcheck([]byte(`{"conversationId":"c","terminationReason":"model_stop","fullyIdle":true}`), &out); err != nil {
			t.Fatal(err)
		}
		var blk stopBlockOut
		if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &blk); err != nil {
			t.Fatalf("agy Stop output is not a decision: %q (%v)", out.String(), err)
		}
		return blk
	}

	m := runningGate(t, store, "sty_agy")
	if blk := stopAgy(); blk.Decision != "continue" || !strings.Contains(blk.Reason, "still running") {
		t.Fatalf("agy still-running gate = %+v, want decision=continue", blk)
	}
	_ = os.WriteFile(store.VerdictPath(m.ID), []byte("accepted plan→in_progress\n"), 0o644)
	_ = store.Finish(m.ID, gatehandle.Result{})
	if blk := stopAgy(); blk.Decision != "continue" || !strings.Contains(blk.Reason, "accepted plan→in_progress") {
		t.Fatalf("agy verdict delivery = %+v, want decision=continue carrying the verdict", blk)
	}
}

// AC3: a run that can no longer finish is reported as died, then the stop is
// allowed — the loop cannot run forever.
func TestStopHookTerminalHandleReportedThenAllowed(t *testing.T) {
	cases := map[string]func(t *testing.T, store *gatehandle.Store) gatehandle.Meta{
		"process gone": func(t *testing.T, store *gatehandle.Store) gatehandle.Meta {
			m, _ := store.Create(gatehandle.Meta{Verb: gateTestVerb, Story: "sty_dead", Argv: []string{"story", "set", "sty_dead"}})
			_ = store.SetPID(m.ID, 0x7ffffff0) // no such process
			return m
		},
		"never started": func(t *testing.T, store *gatehandle.Store) gatehandle.Meta {
			m, _ := store.Create(gatehandle.Meta{Verb: gateTestVerb, Story: "sty_dead", Argv: []string{"story", "set", "sty_dead"}, Started: time.Now().Add(-time.Hour)})
			return m
		},
		"process id reused": func(t *testing.T, store *gatehandle.Store) gatehandle.Meta {
			if runtime.GOOS == "freebsd" || runtime.GOOS == "netbsd" || runtime.GOOS == "openbsd" || runtime.GOOS == "dragonfly" {
				t.Skip("no process creation identity on this platform")
			}
			m := runningGate(t, store, "sty_dead")
			meta, _ := store.Meta(m.ID)
			meta.PIDStart = "a-different-process" // this pid, another process's start
			b, _ := json.Marshal(meta)
			if err := os.WriteFile(store.Dir()+"/"+m.ID+"/meta.json", b, 0o644); err != nil {
				t.Fatal(err)
			}
			return m
		},
		"killed after it started": func(t *testing.T, store *gatehandle.Store) gatehandle.Meta {
			c := exec.Command("sleep", "30")
			if runtime.GOOS == "windows" {
				c = exec.Command("cmd", "/c", "ping -n 30 127.0.0.1")
			}
			if err := c.Start(); err != nil {
				t.Skipf("cannot start a child: %v", err)
			}
			m, _ := store.Create(gatehandle.Meta{Verb: gateTestVerb, Story: "sty_dead", Argv: []string{"story", "set", "sty_dead"}})
			_ = store.SetPID(m.ID, c.Process.Pid)
			_ = c.Process.Kill()
			_ = c.Wait()
			return m
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			store := stopWakeRepo(t, "2s")
			m := mk(t, store)
			blk, ok := stopOnce(t, "")
			if !ok || !strings.Contains(blk.Reason, "DIED") || !strings.Contains(blk.Reason, m.ID) {
				t.Fatalf("a run that can no longer finish was not reported as died: %+v ok=%v", blk, ok)
			}
			if blk, ok := stopOnce(t, ""); ok {
				t.Fatalf("the stop after the died report was blocked again:\n%s", blk.Reason)
			}
		})
	}
}

// A platform that cannot verify liveness blocks once, saying so, and then lets
// the stop go — while the verdict is still delivered when the run finishes, even
// on a stop_hook_active Stop.
func TestStopHookUnverifiedLivenessBlocksOnceThenAllows(t *testing.T) {
	store := stopWakeRepo(t, "50ms")
	t.Setenv(gatehandle.EnvLiveness, "unavailable")
	m := runningGate(t, store, "sty_unverified")

	blk, ok := stopOnce(t, "")
	if !ok || !strings.Contains(blk.Reason, "liveness unavailable on "+runtime.GOOS) || !strings.Contains(blk.Reason, m.ID) {
		t.Fatalf("the first Stop did not say liveness is unavailable: %+v ok=%v", blk, ok)
	}
	for _, marker := range []string{"pending-notified", "unverified-notified"} {
		if _, err := os.Stat(store.Dir() + "/" + m.ID + "/" + marker); err != nil {
			t.Errorf("no %s marker: %v", marker, err)
		}
	}
	// From here the wait bound is far larger than any scheduling delay, so a Stop
	// that returns well under a tenth of it provably did not wait the bound.
	const bound = 5 * time.Second
	t.Setenv(stopGateWaitEnv, bound.String())
	for i := 0; i < 2; i++ {
		start := time.Now()
		if blk, ok := stopOnce(t, ""); ok {
			t.Fatalf("Stop %d after the unverified note blocked again:\n%s", i+2, blk.Reason)
		}
		if took := time.Since(start); took > bound/10 {
			t.Errorf("an unverified run held Stop %d for %s (bound %s)", i+2, took, bound)
		}
	}

	_ = os.WriteFile(store.VerdictPath(m.ID), []byte("accepted plan→in_progress\n"), 0o644)
	_ = store.Finish(m.ID, gatehandle.Result{})
	blk, ok = stopOnce(t, `{"stop_hook_active":true}`)
	if !ok || !strings.Contains(blk.Reason, "accepted plan→in_progress") {
		t.Fatalf("the verdict of a run noted as unverified was lost: %+v ok=%v", blk, ok)
	}
}

// A run started with no readable identity is unverified even when its process
// is plainly alive.
func TestStopHookNoIdentityIsUnverified(t *testing.T) {
	store := stopWakeRepo(t, "50ms")
	m := runningGate(t, store, "sty_noid")
	meta, _ := store.Meta(m.ID)
	meta.PIDStart, meta.IdentityUnavailable = "", true
	b, _ := json.Marshal(meta)
	if err := os.WriteFile(store.Dir()+"/"+m.ID+"/meta.json", b, 0o644); err != nil {
		t.Fatal(err)
	}
	if blk, ok := stopOnce(t, ""); !ok || !strings.Contains(blk.Reason, "liveness unavailable") {
		t.Fatalf("a run with no identity was not reported as unverified: %+v ok=%v", blk, ok)
	}
	if _, ok := stopOnce(t, ""); ok {
		t.Fatal("a run with no identity held a second Stop")
	}
}

// UserPromptSubmit stays finished-only: it never waits and never says a run is
// still going.
func TestPromptHookDoesNotReportRunningGates(t *testing.T) {
	store := stopWakeRepo(t, "50ms")
	m := runningGate(t, store, "sty_prompt_run")
	var out strings.Builder
	if err := runHookPrompt(&out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), m.ID) {
		t.Fatalf("the prompt hook mentioned a run that is still going:\n%s", out.String())
	}
	if _, err := os.Stat(store.Dir() + "/" + m.ID + "/pending-notified"); err == nil {
		t.Error("the prompt hook marked a run notified")
	}
}

// A run's outcome is decided from one look: a run that finishes between the
// running check and the delivery is never both "still running" and delivered as
// nothing.
func TestStopDeliveryDecidesFromOneObservation(t *testing.T) {
	store := stopWakeRepo(t, "0s")
	m := runningGate(t, store, "sty_race")
	// Finish lands after the wait, before the observation: the one look sees it.
	_ = os.WriteFile(store.VerdictPath(m.ID), []byte("accepted\n"), 0o644)
	_ = store.Finish(m.ID, gatehandle.Result{})
	text, delivered := deliverStopGates(store, "", 0)
	if len(delivered) != 1 || !strings.Contains(text, "accepted") || strings.Contains(text, "still running") {
		t.Fatalf("delivered=%v text=%q", delivered, text)
	}
}

// AC5: the wait bound stays below the Stop timeout the scaffold installs, for
// every override an operator can give. Codex is absent by design: it installs no
// Stop hook (TestCompletionNotification_CellsMatchTheScaffold).
func TestStopWaitBelowInstalledHookTimeout(t *testing.T) {
	timeouts := installedStopTimeouts(t)
	if len(timeouts) == 0 {
		t.Fatal("no installed Stop hook to tie the wait bound to")
	}
	if stopGateWaitDefault > stopGateWaitMax {
		t.Fatalf("the default wait %s exceeds the clamp %s", stopGateWaitDefault, stopGateWaitMax)
	}
	for _, env := range []string{"", "10s", "29m", "48h"} {
		t.Setenv(stopGateWaitEnv, env)
		wait := stopGateWait()
		for name, timeout := range timeouts {
			if wait+stopHookMargin > time.Duration(timeout)*time.Second {
				t.Errorf("%s: %s=%q waits %s, too close to the installed Stop timeout %vs", name, stopGateWaitEnv, env, wait, timeout)
			}
		}
	}
}
