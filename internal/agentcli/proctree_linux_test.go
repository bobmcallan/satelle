//go:build linux

package agentcli

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startGroup (sty_2d2c4c03) starts `sh -c script` in its own process group and
// returns the command plus an idempotent cleanup that SIGKILLs the whole group
// (so forked subshells and sleeps die with the root), reaps the root, and
// reports any group member still alive. It is also registered with t.Cleanup.
func startGroup(t *testing.T, script string) (*exec.Cmd, func()) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := cmd.Process.Pid
	done := false
	cleanup := func() {
		if done {
			return
		}
		done = true
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		_ = cmd.Wait()
		if !waitGroupGone(pgid, time.Second) {
			t.Errorf("process group %d still has live members after cleanup", pgid)
		}
	}
	t.Cleanup(cleanup)
	return cmd, cleanup
}

// waitGroupGone polls until no process remains in group pgid (ESRCH).
func waitGroupGone(pgid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if syscall.Kill(-pgid, 0) == syscall.ESRCH {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// groupMembers counts processes in group pgid by scanning /proc/*/stat.
func groupMembers(pgid int) int {
	entries, _ := os.ReadDir("/proc")
	n := 0
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(b)
		rest := strings.Fields(s[strings.LastIndexByte(s, ')')+1:]) // state ppid pgrp ...
		if len(rest) > 2 && rest[2] == strconv.Itoa(pgid) {
			n++
		}
	}
	return n
}

// TestProcTreeGroupCleanupLeavesNoDescendants (sty_2d2c4c03): cleaning up a
// grouped process tree leaves no forked descendant behind.
func TestProcTreeGroupCleanupLeavesNoDescendants(t *testing.T) {
	cmd, cleanup := startGroup(t, "(while :; do :; done) & sleep 30 & wait")
	pgid := cmd.Process.Pid
	deadline := time.Now().Add(2 * time.Second)
	for groupMembers(pgid) < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("descendants never appeared (group has %d members)", groupMembers(pgid))
		}
		time.Sleep(20 * time.Millisecond)
	}
	cleanup()
	if !waitGroupGone(pgid, 2*time.Second) {
		t.Fatalf("process group %d survived cleanup", pgid)
	}
}

// TestProcTreeCPUAdvancesAndIncludesDescendants (sty_db62a3b9): the CPU time
// of a shell whose busy work runs in a forked child still advances the root's
// tree total.
func TestProcTreeCPUAdvancesAndIncludesDescendants(t *testing.T) {
	cmd, _ := startGroup(t, "(while :; do :; done) & wait")
	pid := cmd.Process.Pid

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
	cmd, _ := startGroup(t, "sh -c '"+busy+"'; sleep 30")
	pid := cmd.Process.Pid

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
