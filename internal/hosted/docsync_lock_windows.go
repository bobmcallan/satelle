//go:build windows

package hosted

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockStateFile takes an exclusive lock on f, waiting for any other process
// holding it. The lock is released by unlockStateFile or by closing f.
func lockStateFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, new(windows.Overlapped))
}

func unlockStateFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
