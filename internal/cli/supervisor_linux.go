//go:build linux

package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// supervisor is what owns a running process, derived from KERNEL FACTS only —
// no systemctl, no D-Bus (sty_f20f3f3b). It answers the one question the
// bus-free restart path needs: if this process exits, will something start it
// again?
//
// Persistent means the parent is a systemd manager that survives session loss:
// the SYSTEM manager (pid 1), or a USER manager whose account has lingering
// enabled. A user manager WITHOUT linger dies with the login session, so a
// service it owns is not durably supervised and must not be signalled.
type supervisor struct {
	PID        int
	Name       string
	Persistent bool
}

// lingerEnabled is a hook so tests never depend on the host's linger state.
var lingerEnabled = func(username string) bool {
	if username == "" {
		return false
	}
	_, err := os.Stat(filepath.Join("/var/lib/systemd/linger", username))
	return err == nil
}

// persistentSupervisor reports the supervisor of pid, and whether it will
// respawn a child that exits. ok is false when the parent cannot be read at all
// — an unknown parent is never treated as supervised.
func persistentSupervisor(procRoot string, pid int) (supervisor, bool) {
	ppid, ok := parentPID(procRoot, pid)
	if !ok || ppid <= 0 {
		return supervisor{}, false
	}
	s := supervisor{PID: ppid, Name: processName(procRoot, ppid)}
	if s.Name != "systemd" {
		return s, true // known parent, but not a supervisor that respawns
	}
	if ppid == 1 {
		s.Persistent = true // system manager
		return s, true
	}
	// A user manager only outlives the login session when the account lingers.
	s.Persistent = lingerEnabled(currentUsername())
	return s, true
}

// parentPID reads PPid from procRoot/<pid>/stat.
//
// The comm field (2) is wrapped in parentheses and MAY ITSELF contain spaces and
// parentheses, so splitting the whole line on spaces shifts every later field.
// Parse from the LAST ')' instead — everything after it is fixed-width, and PPid
// is the second token there (state, then ppid).
func parentPID(procRoot string, pid int) (int, bool) {
	raw, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, false
	}
	i := strings.LastIndex(string(raw), ")")
	if i < 0 {
		return 0, false
	}
	fields := strings.Fields(string(raw)[i+1:])
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}
	return ppid, true
}

// processName returns procRoot/<pid>/comm, falling back to argv[0]'s base name
// when comm is unreadable. Empty when neither resolves.
func processName(procRoot string, pid int) string {
	dir := filepath.Join(procRoot, strconv.Itoa(pid))
	if raw, err := os.ReadFile(filepath.Join(dir, "comm")); err == nil {
		if n := strings.TrimSpace(string(raw)); n != "" {
			return n
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil {
		return ""
	}
	arg0, _, _ := strings.Cut(string(raw), "\x00")
	if arg0 = strings.TrimSpace(arg0); arg0 == "" {
		return ""
	}
	return filepath.Base(arg0)
}
