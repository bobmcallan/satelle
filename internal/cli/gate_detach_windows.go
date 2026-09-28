//go:build windows

package cli

import (
	"os/exec"
	"syscall"
)

// detachChild starts the gate run detached from this console and process group,
// so it outlives the command that started it (sty_c4b92c9e).
func detachChild(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	const detachedProcess = 0x00000008
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess
}
