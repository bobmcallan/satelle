//go:build integration && linux

package tests

import (
	"os/exec"
	"syscall"
)

// setServeProcAttr puts the child in its own process group and arms Pdeathsig
// so a go-test timeout / hard kill of the parent cannot leave serve orphans.
//
// Known risk: Go can retire the OS thread that forked, which may deliver
// Pdeathsig early. Happy-path cleanup still group-kills; TestServeDiesWhenParentDies
// proves the parent-death path. Escalate to a supervisor pipe only if that test
// is measured flaky (sty_bf797fa9 plan).
func setServeProcAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}

// killServeGroup signals the whole process group. soft=true → SIGTERM (graceful);
// soft=false → SIGKILL (escalation after Stop's wait). Falls back to Process.Kill
// when the group is already gone.
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

// scanServePIDs is the Linux /proc implementation.
func scanServePIDs() map[int]string {
	return scanServePIDsFromProc("/proc")
}
