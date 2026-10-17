//go:build unix

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// ownGroup puts the child in its own process group, so signalGroup reaches the
// test binaries `go test` starts and not only `go test` itself (sty_3f2a5c78).
func ownGroup(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// signalGroup sends sig to the child's whole process group. With Setpgid the
// group id is the child's pid.
func signalGroup(c *exec.Cmd, sig os.Signal) {
	if s, ok := sig.(syscall.Signal); ok && c.Process != nil {
		_ = syscall.Kill(-c.Process.Pid, s)
	}
}
