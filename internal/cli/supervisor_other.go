//go:build !linux

package cli

// supervisor / persistentSupervisor exist on every platform so the bus-free
// restart path compiles, but /proc-derived supervisor facts are Linux-only.
// restartServiceIfRunningRoot already returns early on non-Linux, so this stub
// is never consulted at runtime (sty_f20f3f3b).
type supervisor struct {
	PID        int
	Name       string
	Persistent bool
}

func persistentSupervisor(procRoot string, pid int) (supervisor, bool) {
	return supervisor{}, false
}
