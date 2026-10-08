package agentstep

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// busyScript writes an executable sh script and returns a command-interface
// Runner for it.
//
// Invariant (sty_2d2c4c03): the command transport kills only the script's own
// pid (no process group), so every body must do its work in that process —
// shell builtins or `exec`, never `&` or `( )` — or the stall kill leaves
// orphans behind.
func busyScript(t *testing.T, body string) agentcli.Runner {
	t.Helper()
	script := filepath.Join(t.TempDir(), "agent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	runner, err := agentcli.RunnerFromBinding(agentcli.InterfaceCommand, script+" --noop")
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

// TestCommandTransportBusySilentNotStalled (sty_db62a3b9 AC1): a one-shot
// command CLI that prints nothing for longer than idle_timeout while its
// process burns CPU is NOT cancelled; its envelope at exit comes back intact.
func TestCommandTransportBusySilentNotStalled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("CPU liveness probe is linux-only; other platforms stay strict")
	}
	// time-subject: the 300ms idle window is the subject. The busy loop must outlast
	// it for the test to mean anything; a machine fast enough to finish sooner skips
	// (below) instead of passing vacuously.
	// Shell builtins only: the CPU is the shell's own, no forked children.
	// idle_timeout is generous relative to the 10ms CPU-tick granularity so a
	// loaded CI box that starves the shell for a moment is not a false stall.
	runner := busyScript(t, "i=0\nwhile [ $i -lt 1500000 ]; do i=$((i+1)); done\necho '{\"result\":\"ok\"}'\n")
	g := New(runner, fakeDocs{}, "/repo", "")

	start := time.Now()
	out, _, err := g.runOnceBusy(context.Background(), runner, agentcli.Request{SystemPrompt: "x"}, 0, 300*time.Millisecond, 30*time.Second)
	if err != nil {
		t.Fatalf("busy silent run must not stall: %v", err)
	}
	if !strings.Contains(string(out), "ok") {
		t.Errorf("out = %q, want the envelope's result", out)
	}
	if elapsed := time.Since(start); elapsed < 400*time.Millisecond {
		t.Skipf("busy loop finished in %v, too fast to exceed idle_timeout on this machine", elapsed)
	}
}

// TestCommandTransportSleepingStillStalls (AC2): with the busy cap enabled, a
// silent process that burns no CPU is still cancelled at idle_timeout.
//
// time-subject: the 80ms idle window is the subject; the process sleeps 60s so
// "killed" and "ran to completion" stay far apart whatever the machine speed.
func TestCommandTransportSleepingStillStalls(t *testing.T) {
	runner := busyScript(t, "exec sleep 60\n")
	g := New(runner, fakeDocs{}, "/repo", "")

	start := time.Now()
	_, _, err := g.runOnceBusy(context.Background(), runner, agentcli.Request{SystemPrompt: "x"}, 0, 80*time.Millisecond, 5*time.Second)
	var se *StallError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *StallError", err)
	}
	if se.BusyCapExceeded {
		t.Error("a sleeping process stalled on idle, not on the busy cap")
	}
	if elapsed := time.Since(start); elapsed >= 30*time.Second {
		t.Errorf("sleeping process was not killed promptly: %v", elapsed)
	}
}

// TestCommandTransportBusyCapStillStalls: a process that spins forever is
// stalled once the busy cap (from its last real event) runs out.
//
// time-subject: the idle window and the busy cap are the subject. Both are wide
// (300ms idle, 900ms cap) so a runner that starves the spinning shell for a few
// ticks stalls on the cap rather than on idle, which is what the test asserts.
func TestCommandTransportBusyCapStillStalls(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("CPU liveness probe is linux-only")
	}
	runner := busyScript(t, "while :; do :; done\n")
	g := New(runner, fakeDocs{}, "/repo", "")

	start := time.Now()
	_, _, err := g.runOnceBusy(context.Background(), runner, agentcli.Request{SystemPrompt: "x"}, 0, 300*time.Millisecond, 900*time.Millisecond)
	var se *StallError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *StallError", err)
	}
	if !se.BusyCapExceeded || !strings.Contains(se.Error(), "busy cap exceeded") {
		t.Errorf("stall = %q, want the busy cap named", se.Error())
	}
	if elapsed := time.Since(start); elapsed >= 15*time.Second {
		t.Errorf("busy cap did not stop the spin promptly: %v", elapsed)
	}
}
