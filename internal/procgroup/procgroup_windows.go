//go:build windows

package procgroup

import "os/exec"

// Windows has no POSIX process groups, and the Unix Setpgid/Kill pair does not
// exist in its syscall package. Killing the child directly is the portable
// behaviour available here: the caller still bounds and terminates what it
// started, it simply cannot guarantee the same reach over grandchildren that a
// process-group signal gives on Unix.

// Set is a no-op on Windows.
func Set(_ *exec.Cmd) {}

// Kill terminates the spawned process.
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
