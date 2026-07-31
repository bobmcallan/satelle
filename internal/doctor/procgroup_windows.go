//go:build windows

package doctor

import "os/exec"

// Process-group control for live probes, Windows flavour.
//
// Windows has no POSIX process groups, and the Unix Setpgid/Kill pair does not
// exist in its syscall package. Killing the child directly is the portable
// behaviour available here: the probe still bounds and terminates what it
// started, it simply cannot guarantee the same reach over grandchildren that a
// process-group signal gives on Unix.

// setProcessGroup is a no-op on Windows.
func setProcessGroup(_ *exec.Cmd) {}

// killGroup terminates the spawned process.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
