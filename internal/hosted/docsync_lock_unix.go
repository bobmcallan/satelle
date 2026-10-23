//go:build unix

package hosted

import (
	"os"
	"syscall"
)

// lockStateFile takes an exclusive advisory lock on f, waiting for any other
// process holding it. The lock is released by unlockStateFile or by closing f.
func lockStateFile(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			return err
		}
	}
}

func unlockStateFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
