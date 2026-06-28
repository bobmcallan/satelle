//go:build !linux

package cli

import "os/exec"

// setChildDeathSignal is a no-op on platforms without a parent-death signal;
// children are cleaned up by the supervisor's deferred kill and context cancel.
func setChildDeathSignal(cmd *exec.Cmd) {}
