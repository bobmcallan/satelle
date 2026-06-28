//go:build linux

package cli

import (
	"os/exec"
	"syscall"
)

// setChildDeathSignal makes a supervised child receive SIGKILL if the parent
// (this supervisor) dies for any reason — including a hard kill where deferred
// cleanup never runs — so children can't orphan. Linux-only (Pdeathsig).
func setChildDeathSignal(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
