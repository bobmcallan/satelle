//go:build !unix

package main

import (
	"os"
	"os/exec"
)

// ownGroup is a no-op where process groups are not available.
func ownGroup(*exec.Cmd) {}

// signalGroup falls back to signalling the child alone: the group kill that
// reaches the test binaries is Unix-only.
func signalGroup(c *exec.Cmd, sig os.Signal) {
	if c.Process != nil {
		_ = c.Process.Signal(sig)
	}
}
