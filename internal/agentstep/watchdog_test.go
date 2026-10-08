package agentstep

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// TestWatchdogTouchResetsIdleClock: a real event before idle elapses keeps the
// watchdog from firing; Snapshot reports the last touched label.
//
// time-subject: the idle clock is what is under test. The idle window is wide
// (500ms against 20ms touch gaps) so a scheduler stall on a slow runner cannot
// let it lapse between touches, and the loop outlasts the window by half again,
// which is what proves the touches — not luck — kept it from firing.
func TestWatchdogTouchResetsIdleClock(t *testing.T) {
	wd := NewWatchdog(500 * time.Millisecond)
	ctx, stop := wd.Start(context.Background())
	defer stop()

	deadline := time.Now().Add(750 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond) // time-subject: touch cadence inside the idle window
		wd.Touch("tool: Bash")
	}
	select {
	case <-ctx.Done():
		t.Fatalf("watchdog fired despite repeated touches: cause=%v", context.Cause(ctx))
	default:
	}
	snap := wd.Snapshot()
	if snap.LastEvent != "tool: Bash" {
		t.Errorf("Snapshot().LastEvent = %q, want %q", snap.LastEvent, "tool: Bash")
	}
	if snap.EventCount == 0 {
		t.Error("Snapshot().EventCount = 0, want > 0")
	}
}

// TestWatchdogFiresAfterIdle: with no touches, the watchdog cancels its
// derived context with a *StallError once idle elapses.
//
// time-subject: the 50ms idle window is the subject; the 10s select below only
// bounds how long a starved runner is given to deliver the stall.
func TestWatchdogFiresAfterIdle(t *testing.T) {
	wd := NewWatchdog(50 * time.Millisecond)
	ctx, stop := wd.Start(context.Background())
	defer stop()
	select {
	case <-ctx.Done():
		se, ok := context.Cause(ctx).(*StallError)
		if !ok {
			t.Fatalf("context.Cause(ctx) = %v, want a *StallError", context.Cause(ctx))
		}
		if se.LastEvent != "start" {
			t.Errorf("StallError.LastEvent = %q, want %q", se.LastEvent, "start")
		}
	case <-time.After(testutil.WaitBudget):
		t.Fatal("watchdog never fired")
	}
}

// TestWatchdogTouchEventIgnoresHeartbeat: EventHeartbeat must not reset the
// idle clock — only a real event does (sty_752c4ef2 evidence #2).
//
// time-subject: the 60ms idle window is the subject; the 10s select below only
// bounds how long a starved runner is given to deliver the stall.
func TestWatchdogTouchEventIgnoresHeartbeat(t *testing.T) {
	wd := NewWatchdog(60 * time.Millisecond)
	ctx, stop := wd.Start(context.Background())
	defer stop()

	stopHeartbeats := make(chan struct{})
	defer close(stopHeartbeats)
	go func() {
		t := time.NewTicker(10 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stopHeartbeats:
				return
			case <-t.C:
				wd.TouchEvent(agentcli.Event{Kind: agentcli.EventHeartbeat})
			}
		}
	}()

	select {
	case <-ctx.Done():
		if _, ok := context.Cause(ctx).(*StallError); !ok {
			t.Fatalf("context.Cause(ctx) = %v, want a *StallError", context.Cause(ctx))
		}
	case <-time.After(testutil.WaitBudget):
		t.Fatal("a heartbeat-only stream must still stall")
	}
}

// TestWatchdogKillsRealSubprocessPromptly (sty_752c4ef2 AC1/AC2): a Watchdog's
// derived context, handed straight to exec.CommandContext exactly as every
// agentcli transport does, kills a real hung subprocess promptly on stall —
// no transport-specific code required. Uses exec so the killed PID IS the
// sleeping process (a "sleep 5 &" child would inherit the parent's stdout fd
// and keep a pipe reader blocked past the kill, which is a pipe-EOF nuance of
// the test's own process tree, not something the watchdog needs to handle).
//
// time-subject: the 80ms idle window is the subject. The subprocess sleeps far
// longer than any runner needs to deliver the stall, so "killed" and "ran to
// completion" stay separated by a wide margin whatever the machine speed.
func TestWatchdogKillsRealSubprocessPromptly(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "silent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	wd := NewWatchdog(80 * time.Millisecond)
	ctx, stop := wd.Start(context.Background())
	defer stop()
	start := time.Now()
	cmd := exec.CommandContext(ctx, script)
	err := cmd.Run()
	elapsed := time.Since(start)
	if elapsed >= 30*time.Second {
		t.Fatalf("exec.CommandContext did not kill the process on watchdog cancel: elapsed=%v err=%v", elapsed, err)
	}
	if _, ok := context.Cause(ctx).(*StallError); !ok {
		t.Errorf("context.Cause(ctx) = %v, want a *StallError", context.Cause(ctx))
	}
}

// TestWatchdogProgressBoundedByBusyCap (sty_db62a3b9): an EventProgress resets
// the idle clock only while under the busy cap from the last real event, and
// never counts as a real event.
//
// time-subject: the idle window and the busy cap are the subject. Both are wide
// against the 20ms progress cadence (300ms idle, 900ms cap) so a scheduler stall
// cannot lapse the idle clock, and the first phase stays well inside the cap.
func TestWatchdogProgressBoundedByBusyCap(t *testing.T) {
	if isRealEvent(agentcli.EventProgress) {
		t.Fatal("EventProgress must not be a real event")
	}
	wd := NewWatchdog(300 * time.Millisecond)
	wd.SetBusyCap(900 * time.Millisecond)
	ctx, stop := wd.Start(context.Background())
	defer stop()

	ev := agentcli.Event{Kind: agentcli.EventProgress}
	for end := time.Now().Add(450 * time.Millisecond); time.Now().Before(end); {
		time.Sleep(20 * time.Millisecond) // time-subject: progress cadence inside the idle window
		wd.TouchEvent(ev)
	}
	if ctx.Err() != nil {
		t.Fatalf("watchdog fired under the busy cap: %v", context.Cause(ctx))
	}
	if snap := wd.Snapshot(); snap.EventCount != 0 || snap.LastEvent != "start" {
		t.Errorf("progress leaked into real-event state: %+v", snap)
	}
	// Past the cap, progress no longer helps. Keep reporting it until the stall
	// lands; the outer bound only caps a hung test.
	for end := time.Now().Add(testutil.WaitBudget); time.Now().Before(end) && ctx.Err() == nil; {
		time.Sleep(20 * time.Millisecond) // time-subject: progress cadence past the busy cap
		wd.TouchEvent(ev)
	}
	se := StallCause(ctx)
	if se == nil || !se.BusyCapExceeded {
		t.Fatalf("cause = %v, want a stall with the busy cap exceeded", context.Cause(ctx))
	}
}
