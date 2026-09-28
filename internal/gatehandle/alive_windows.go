//go:build windows

package gatehandle

// pidAlive cannot tell on windows without opening the process, so a run is
// treated as alive: a hook then waits out its bound instead of delivering a
// live run as dead.
func pidAlive(pid int) bool { return pid > 0 }
