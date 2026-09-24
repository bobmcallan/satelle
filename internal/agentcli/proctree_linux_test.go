//go:build linux

package agentcli

import (
	"os/exec"
	"testing"
	"time"
)

// TestProcTreeCPUAdvancesAndIncludesDescendants (sty_db62a3b9): the CPU time
// of a shell whose busy work runs in a forked child still advances the root's
// tree total.
func TestProcTreeCPUAdvancesAndIncludesDescendants(t *testing.T) {
	cmd := exec.Command("sh", "-c", "(while :; do :; done) & wait")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	first, ok := procTreeCPU(pid)
	if !ok {
		t.Fatal("running root not found in /proc")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		if cur, ok := procTreeCPU(pid); ok && cur > first {
			return
		}
	}
	t.Fatalf("tree CPU never advanced past %d ticks (descendant not counted?)", first)
}

// TestProcTreeCPUKeepsReapedChildCPU (sty_db62a3b9): CPU burned by a child that
// has already exited and been reaped stays in the tree total, so the total does
// not fall when the child ends (which would make the next sample look like no
// progress). The root shell itself burns almost nothing: the CPU is spent in an
// inner shell that the root runs to completion and reaps before sleeping.
func TestProcTreeCPUKeepsReapedChildCPU(t *testing.T) {
	busy := `end=$(($(date +%s)+2)); while [ $(date +%s) -lt $end ]; do :; done`
	cmd := exec.Command("sh", "-c", "sh -c '"+busy+"'; sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	var peak uint64
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		cur, ok := procTreeCPU(pid)
		if !ok {
			t.Fatal("running root not found in /proc")
		}
		if cur < peak {
			t.Fatalf("tree CPU dropped from %d to %d ticks after the child was reaped", peak, cur)
		}
		peak = cur
	}
	if peak < 20 {
		t.Fatalf("only %d ticks observed; the busy child's CPU was not counted", peak)
	}
}

// TestProcTreeCPUGoneRoot: a reaped root reports ok=false.
func TestProcTreeCPUGoneRoot(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if _, ok := procTreeCPU(cmd.Process.Pid); ok {
		t.Error("reaped process must report ok=false")
	}
}
