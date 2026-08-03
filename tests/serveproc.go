//go:build integration

package tests

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// serveRegistry tracks suite-owned serve pids so TestMain can prove none survive
// a full run (sty_bf797fa9 AC1). Baseline host serves are excluded by identity.
var (
	serveRegMu sync.Mutex
	serveReg   = map[int]string{} // pid → short cmdline for diagnostics
)

func registerServe(pid int, cmdline string) {
	if pid <= 0 {
		return
	}
	serveRegMu.Lock()
	serveReg[pid] = cmdline
	serveRegMu.Unlock()
}

func unregisterServe(pid int) {
	if pid <= 0 {
		return
	}
	serveRegMu.Lock()
	delete(serveReg, pid)
	serveRegMu.Unlock()
}

func registeredServeSnapshot() map[int]string {
	serveRegMu.Lock()
	defer serveRegMu.Unlock()
	out := make(map[int]string, len(serveReg))
	for k, v := range serveReg {
		out[k] = v
	}
	return out
}

// ServeHandle is a satelle serve process started by the suite.
// Cleanup always reaps the process group; Stop is safe for mid-test restarts.
type ServeHandle struct {
	Cmd  *exec.Cmd
	Base string // http://127.0.0.1:<port>
	Port string

	mu      sync.Mutex
	stopped bool
}

// StartServe launches bin as `serve` with args (e.g. --port, --addr, --no-watch).
// dir is the working directory; env is the full environment (required: SATELLE_HOME).
// The child is put in its own process group with a parent-death signal on Linux
// so go-test timeouts and hard kills cannot leave orphans (sty_bf797fa9 AC2).
//
// On every exit path — including t.Fatal and suite panic cleanup — the process
// group is signalled and waited. Call Stop() for intentional mid-test restarts.
func StartServe(t *testing.T, bin, dir string, env []string, args ...string) *ServeHandle {
	t.Helper()
	full := append([]string{"serve"}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	setServeProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	h := &ServeHandle{Cmd: cmd}
	if cmd.Process != nil {
		registerServe(cmd.Process.Pid, strings.Join(full, " "))
	}
	t.Cleanup(func() { h.Stop() })

	// Derive Base/Port from args when present so callers can waitHealthy without
	// re-parsing. Prefer explicit --port / --addr; otherwise leave empty.
	h.Port, h.Base = serveAddrFromArgs(args)
	return h
}

// StartServeHealthy is StartServe plus waitHealthy on /healthz.
func StartServeHealthy(t *testing.T, bin, dir string, env []string, wait time.Duration, args ...string) *ServeHandle {
	t.Helper()
	h := StartServe(t, bin, dir, env, args...)
	if h.Base == "" {
		t.Fatal("StartServeHealthy requires --port in args so Base is known")
	}
	if wait <= 0 {
		wait = 10 * time.Second
	}
	if !waitHealthy(t, h.Base+"/healthz", wait) {
		h.Stop()
		t.Fatal("serve did not become healthy")
	}
	if h.Cmd.ProcessState != nil && h.Cmd.ProcessState.Exited() {
		t.Fatal("serve exited before becoming healthy (port bind failed?)")
	}
	return h
}

// Stop reaps the serve process group. Idempotent.
func (h *ServeHandle) Stop() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return
	}
	h.stopped = true
	if h.Cmd == nil || h.Cmd.Process == nil {
		return
	}
	pid := h.Cmd.Process.Pid
	defer unregisterServe(pid)
	// Soft TERM first so serve can flush; escalate to SIGKILL on timeout (AC2).
	killServeGroup(h.Cmd, true)
	done := make(chan struct{})
	go func() {
		_, _ = h.Cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		// Escalation: process-group SIGKILL, not a second TERM.
		killServeGroup(h.Cmd, false)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
}

// AttachOutput routes stdout/stderr to w before Start (call only on a handle
// built manually). Prefer StartServeWithOutput for capture-from-start cases.
func (h *ServeHandle) AttachOutput(w io.Writer) {
	if h == nil || h.Cmd == nil {
		return
	}
	h.Cmd.Stdout = w
	h.Cmd.Stderr = w
}

// StartServeWithOutput is StartServe with combined stdout/stderr capture.
func StartServeWithOutput(t *testing.T, bin, dir string, env []string, out io.Writer, args ...string) *ServeHandle {
	t.Helper()
	full := append([]string{"serve"}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	if out != nil {
		cmd.Stdout = out
		cmd.Stderr = out
	}
	setServeProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	h := &ServeHandle{Cmd: cmd}
	if cmd.Process != nil {
		registerServe(cmd.Process.Pid, strings.Join(full, " "))
	}
	t.Cleanup(func() { h.Stop() })
	h.Port, h.Base = serveAddrFromArgs(args)
	return h
}

func serveAddrFromArgs(args []string) (port, base string) {
	addr := "127.0.0.1"
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--port" && i+1 < len(args):
			port = args[i+1]
			i++
		case strings.HasPrefix(a, "--port="):
			port = strings.TrimPrefix(a, "--port=")
		case a == "--addr" && i+1 < len(args):
			addr = args[i+1]
			i++
		case strings.HasPrefix(a, "--addr="):
			addr = strings.TrimPrefix(a, "--addr=")
		}
	}
	if port != "" {
		base = fmt.Sprintf("http://%s:%s", addr, port)
	}
	return port, base
}

// --- suite-level serve process inventory (sty_bf797fa9 AC1 / sty_948a2d42) ---

// liveServePIDs returns pid → cmdline for processes that look like a satelle
// serve. On Linux this is a full /proc sweep; off Linux it is the suite
// registry of pids this run started (see serveScanKind).
func liveServePIDs() map[int]string {
	return scanServePIDs()
}

// registryLiveServePIDs returns registered suite-owned serve pids that still
// respond to signal 0 (alive). Portable — no /proc. Used off Linux as the
// inventory, and in tests that deliberately leak a registered pid (sty_948a2d42).
func registryLiveServePIDs() map[int]string {
	out := map[int]string{}
	for pid, cmd := range registeredServeSnapshot() {
		if err := syscall.Kill(pid, 0); err == nil {
			out[pid] = cmd
		}
	}
	return out
}

// serveScanCoverage describes what the current platform's inventory can see.
// Always non-empty so a green suite cannot look like an unchecked no-op.
func serveScanCoverage() string {
	return serveScanCoverageFor(serveScanKind)
}

func serveScanCoverageFor(kind string) string {
	switch kind {
	case "proc":
		return "/proc sweep — all satelle serve processes on this host"
	case "registry":
		return "registry-only — pids this suite started; strays not started by this suite are NOT checked (no /proc); hard parent-kill can still leave a process group (no portable Pdeathsig)"
	default:
		return "unknown serve-scan kind " + kind
	}
}

// serveLeakReport compares before/after pid sets. Returns leaks (nil when
// clean) and always a non-empty msg: either a FATAL leak report or a coverage
// line so a green run still says whether detection was full or partial
// (sty_948a2d42 AC1). Host baseline pids are excluded.
func serveLeakReport(before map[int]string) (leaks map[int]string, msg string) {
	after := liveServePIDs()
	reg := registeredServeSnapshot()
	leaks = map[int]string{}
	for pid, cmd := range after {
		if _, ok := before[pid]; ok {
			continue // pre-existing (operator serve, etc.)
		}
		// Suite-owned if still in the registry OR cmdline mentions testBin.
		if _, owned := reg[pid]; owned || isSuiteServeCmdline(cmd) {
			leaks[pid] = cmd
		}
	}
	// Registry entries that never unregistered and are still alive.
	for pid, cmd := range reg {
		if _, alive := after[pid]; alive {
			if _, ok := before[pid]; !ok {
				leaks[pid] = cmd
			}
		}
	}
	coverage := serveScanCoverage()
	if len(leaks) == 0 {
		switch serveScanKind {
		case "proc":
			return nil, fmt.Sprintf("serve-leak detection: OK (no leaks; %s, before=%d after=%d)\n",
				coverage, len(before), len(after))
		default:
			return nil, fmt.Sprintf("serve-leak detection: PARTIAL (%s) — a green result here is unverified for strays (before=%d after=%d)\n",
				coverage, len(before), len(after))
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "serve-leak detection: FAILED (%s)\n", coverage)
	fmt.Fprintf(&b, "FATAL: integration suite left %d satelle serve process(es) alive (before=%d after=%d).\n",
		len(leaks), len(before), len(after))
	for pid, cmd := range leaks {
		fmt.Fprintf(&b, "  pid=%d %s\n", pid, cmd)
	}
	return leaks, b.String()
}

func isSuiteServeCmdline(cmd string) bool {
	if testBin != "" && strings.Contains(cmd, testBin) {
		return true
	}
	// Temp build path from TestMain: …/satelle-itest-…/satelle
	if strings.Contains(cmd, "satelle-itest") && strings.Contains(cmd, "serve") {
		return true
	}
	return false
}

// killLeakedServes SIGKILLs survivors so one run does not poison the next.
func killLeakedServes(leaks map[int]string) {
	for pid := range leaks {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		// Also try process-group kill in case children remain.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

// scanServePIDsFromProc walks /proc/*/cmdline. Extracted for tests.
func scanServePIDsFromProc(procRoot string) map[int]string {
	out := map[int]string{}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return out
	}
	self := os.Getpid()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 || pid == self {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		// cmdline is NUL-separated argv.
		parts := strings.Split(string(raw), "\x00")
		if len(parts) > 0 && parts[len(parts)-1] == "" {
			parts = parts[:len(parts)-1]
		}
		if len(parts) < 2 {
			continue
		}
		joined := strings.Join(parts, " ")
		if !cmdlineLooksLikeServe(parts, joined) {
			continue
		}
		out[pid] = joined
	}
	return out
}

func cmdlineLooksLikeServe(parts []string, joined string) bool {
	// Match `…/satelle serve …` or bare `satelle serve`.
	hasServe := false
	for _, p := range parts {
		if p == "serve" {
			hasServe = true
			break
		}
	}
	if !hasServe {
		return false
	}
	base := filepath.Base(parts[0])
	if base == "satelle" || base == "satelle-serve" || strings.HasPrefix(base, "satelle") {
		return true
	}
	// Copied pin binaries (local_binary_test) still run serve.
	if strings.Contains(joined, "serve") && (strings.Contains(joined, "satelle") || strings.Contains(parts[0], "satelle")) {
		return true
	}
	return false
}
