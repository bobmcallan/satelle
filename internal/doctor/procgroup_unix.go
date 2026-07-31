//go:build !windows

package doctor

import (
	"os/exec"
	"syscall"
)

// Process-group control for live probes, Unix flavour.
//
// A probe must never leak a provider process. Peers commonly spawn children of
// their own (an `npx` wrapper, a shim), so killing only the direct child would
// leave those behind holding the pipe. Putting the child in its own process
// group lets one signal take down the whole tree.

// setProcessGroup puts the child in its own process group.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup signals the whole process group, falling back to the process itself
// when the group is unavailable (the child may already have been reaped).
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
