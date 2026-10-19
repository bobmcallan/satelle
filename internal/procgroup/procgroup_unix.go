//go:build !windows

package procgroup

import (
	"os/exec"
	"syscall"
)

// Set puts the child in its own process group, so one signal reaches the whole
// tree: peers commonly spawn children of their own (an `npx` wrapper, a shim,
// a host CLI's exec session), and killing only the direct child would leave
// those behind holding the pipe. Call it before Start.
func Set(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// Kill signals the whole process group, falling back to the process itself when
// the group is unavailable (the child may already have been reaped).
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
