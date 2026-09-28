//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

// detachChild starts the gate run in its own session, so it survives the
// command that started it — and the harness's cleanup of that command's process
// group — while the gate finishes (sty_c4b92c9e). Unlike setChildDeathSignal it
// asks for the opposite of "die with the parent".
func detachChild(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}
