//go:build !windows

package gatehandle

import (
	"errors"
	"syscall"
)

// pidAlive is the signal-0 existence check. EPERM means the process exists but
// belongs to someone else, which is alive for this purpose. Unlike lease
// liveness it does not lean on /proc, so a run that died is noticed on every
// unix, not only Linux.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
