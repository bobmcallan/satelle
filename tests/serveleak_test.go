//go:build integration

package tests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestServeDiesWhenParentDies proves AC2's timeout/hard-kill path: when the
// parent exits without running Go cleanups, the serve child must still die
// (Pdeathsig on Linux). A plain t.Fatal after StartServe only exercises
// t.Cleanup, which already existed before this story.
func TestServeDiesWhenParentDies(t *testing.T) {
	if _, err := os.Stat("/proc"); err != nil {
		t.Skip("no /proc — parent-death signal is Linux-only")
	}
	home := t.TempDir()
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Intermediary: start serve with Setpgid+Pdeathsig, print its pid, then
	// os.Exit(1) without Wait/Kill — mimicking a go-test hard timeout.
	port := freeListenPort(t)
	helper := filepath.Join(t.TempDir(), "orphan-parent.go")
	src := fmt.Sprintf(`package main
import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)
func main() {
	cmd := exec.Command(%q, "serve", "--addr", "127.0.0.1", "--port", %q, "--no-watch")
	cmd.Dir = %q
	cmd.Env = append(os.Environ(), "SATELLE_HOME="+%q)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "start:", err)
		os.Exit(2)
	}
	fmt.Println(cmd.Process.Pid)
	// Exit without waiting — cleanups never run. Child must die via Pdeathsig.
	os.Exit(1)
}
`, testBin, port, repo, home)
	if err := os.WriteFile(helper, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("go", "run", helper).CombinedOutput()
	// Parent exits 1 by design; we only need the printed pid.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var childPID int
	for _, ln := range lines {
		if p, perr := strconv.Atoi(strings.TrimSpace(ln)); perr == nil && p > 0 {
			childPID = p
		}
	}
	if childPID == 0 {
		t.Fatalf("parent did not print serve pid (err=%v out=%q)", err, out)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, statErr := os.Stat(fmt.Sprintf("/proc/%d", childPID))
		if statErr != nil {
			return // child is gone — pass
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
			t.Fatalf("serve pid %d still alive 5s after parent exit without cleanup (Pdeathsig failed?)", childPID)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestStartServeCleanupOnSubtestExit exercises the t.Cleanup path: when a
// subtest ends (success or fail), StartServe's registered cleanup reaps the
// process group and unregisters the pid. Parent-death without cleanup is
// covered by TestServeDiesWhenParentDies.
func TestStartServeCleanupOnSubtestExit(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	port := freeListenPort(t)
	env := append(os.Environ(), "SATELLE_HOME="+home)

	var pid int
	t.Run("spawn", func(t *testing.T) {
		h := StartServeHealthy(t, testBin, repo, env, 8*time.Second,
			"--addr", "127.0.0.1", "--port", port, "--no-watch")
		if h.Cmd.Process != nil {
			pid = h.Cmd.Process.Pid
		}
		// Subtest ends; t.Cleanup from StartServe runs and reaps.
	})
	if pid == 0 {
		t.Fatal("subtest did not start serve")
	}
	// After subtest cleanup, pid must be gone and unregistered.
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
		t.Fatalf("serve pid %d still alive after subtest cleanup", pid)
	}
	if _, ok := registeredServeSnapshot()[pid]; ok {
		t.Fatalf("serve pid %d still in registry after cleanup", pid)
	}
}

// TestServeLeakReportAnnouncesCoverage pins sty_948a2d42 AC1: a clean
// serveLeakReport always returns a non-empty coverage message, and both
// modes (proc / registry) are exercisable from the formatting helper.
func TestServeLeakReportAnnouncesCoverage(t *testing.T) {
	// Current platform always announces.
	leaks, msg := serveLeakReport(map[int]string{})
	if len(leaks) != 0 {
		// Registry may have leftover pids from a parallel failure — still require msg.
		t.Logf("unexpected leaks in clean-ish report: %v", leaks)
	}
	if msg == "" {
		t.Fatal("serveLeakReport returned empty msg on clean comparison — silent no-op")
	}
	if !strings.Contains(msg, "serve-leak detection:") {
		t.Fatalf("msg missing coverage prefix: %q", msg)
	}
	// Both mode strings are non-empty and distinct.
	proc := serveScanCoverageFor("proc")
	reg := serveScanCoverageFor("registry")
	if proc == "" || reg == "" || proc == reg {
		t.Fatalf("coverage strings bad: proc=%q reg=%q", proc, reg)
	}
	if !strings.Contains(reg, "registry-only") || !strings.Contains(reg, "NOT checked") {
		t.Fatalf("registry coverage must name partial check and residual gap: %q", reg)
	}
	if !strings.Contains(reg, "Pdeathsig") {
		t.Fatalf("registry coverage must name the hard-parent-kill gap (AC2): %q", reg)
	}
}

// TestRegistryInventoryDetectsLeakedServe proves sty_948a2d42 AC3: the portable
// registry inventory detects a deliberately leaked suite-owned serve. Not
// Linux-gated — the registry is platform-independent.
func TestRegistryInventoryDetectsLeakedServe(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	port := freeListenPort(t)
	env := append(os.Environ(), "SATELLE_HOME="+home)

	// Start without t.Cleanup reaping: use raw start + register, then stop
	// only after we have asserted the leak. Pre-register a Stop on defer so a
	// failure still reaps.
	full := []string{"serve", "--addr", "127.0.0.1", "--port", port, "--no-watch"}
	cmd := exec.Command(testBin, full...)
	cmd.Dir = repo
	cmd.Env = env
	setServeProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	registerServe(pid, strings.Join(full, " "))
	h := &ServeHandle{Cmd: cmd, Port: port, Base: "http://127.0.0.1:" + port}
	defer func() {
		h.Stop()
		unregisterServe(pid)
	}()

	// Do not call h.Stop yet — deliberate "leak" still in registry.
	live := registryLiveServePIDs()
	if _, ok := live[pid]; !ok {
		t.Fatalf("registryLiveServePIDs missing deliberately leaked pid %d; got %v", pid, live)
	}
	// serveLeakReport against an empty before set must flag it.
	leaks, msg := serveLeakReport(map[int]string{})
	if _, ok := leaks[pid]; !ok {
		t.Fatalf("serveLeakReport did not flag leaked pid %d; leaks=%v msg=%q", pid, leaks, msg)
	}
	if !strings.Contains(msg, "FATAL") {
		t.Fatalf("leak msg should be FATAL: %q", msg)
	}
}

// TestScanServePIDsMatchesRunningServe is a unit-ish check of the /proc scanner.
func TestScanServePIDsMatchesRunningServe(t *testing.T) {
	if _, err := os.Stat("/proc"); err != nil {
		t.Skip("no /proc")
	}
	home := t.TempDir()
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	port := freeListenPort(t)
	env := append(os.Environ(), "SATELLE_HOME="+home)
	h := StartServeHealthy(t, testBin, repo, env, 8*time.Second,
		"--addr", "127.0.0.1", "--port", port, "--no-watch")
	found := liveServePIDs()
	if _, ok := found[h.Cmd.Process.Pid]; !ok {
		t.Fatalf("liveServePIDs missing pid %d; got %v", h.Cmd.Process.Pid, found)
	}
	h.Stop()
	// After stop, that pid should vanish from the scan.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := liveServePIDs()[h.Cmd.Process.Pid]; !ok {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("pid %d still visible after Stop", h.Cmd.Process.Pid)
}
