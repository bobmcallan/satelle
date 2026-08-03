//go:build integration && !linux

package tests

import (
	"os/exec"
	"syscall"
)

// serveScanKind identifies how liveServePIDs observes processes on this platform.
// "registry" = suite-owned pids only (no /proc). A green suite is PARTIAL for
// strays not started by this suite (sty_948a2d42).
const serveScanKind = "registry"

// setServeProcAttr puts the child in its own process group so cleanup can
// group-kill (portable Setpgid on darwin/BSD). There is no portable parent-death
// signal equivalent of Linux Pdeathsig — a hard go-test SIGKILL of the parent
// can still leave the group unsignalled. That residual gap is named in the
// coverage line and in the decisions doc (sty_948a2d42 AC2/AC4); we do not arm
// a supervisor pipe or product-side parent-death for the harness alone.
func setServeProcAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killServeGroup signals the whole process group (soft=SIGTERM, hard=SIGKILL),
// falling back to Process.Kill when the group is already gone.
func killServeGroup(cmd *exec.Cmd, soft bool) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	sig := syscall.SIGKILL
	if soft {
		sig = syscall.SIGTERM
	}
	if err := syscall.Kill(-pid, sig); err != nil {
		if soft {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		_ = cmd.Process.Kill()
	}
}

// scanServePIDs returns suite-registered serves that are still alive (signal 0).
// No /proc host sweep off Linux — strays not started by this suite are not seen.
func scanServePIDs() map[int]string {
	return registryLiveServePIDs()
}
