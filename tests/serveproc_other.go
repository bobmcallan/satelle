//go:build integration && !linux

package tests

import "os/exec"

// setServeProcAttr is a no-op off Linux: no Pdeathsig / portable Setpgid pair.
// Cleanup still uses Process.Kill via killServeGroup.
func setServeProcAttr(cmd *exec.Cmd) {}

func killServeGroup(cmd *exec.Cmd, soft bool) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = soft
	_ = cmd.Process.Kill()
}

// scanServePIDs is a documented no-op off Linux (no /proc).
func scanServePIDs() map[int]string {
	return map[int]string{}
}
