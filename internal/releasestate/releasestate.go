// Package releasestate is a leaf: the machine-wide cache of the latest satelle
// and satelled releases (sty_15a374a8). `satelle update` and `update --check`
// write it after resolving the releases over the network; the SessionStart hook
// only ever reads it (plus a debounce stamp), so a session start never waits on
// the network.
//
// One file, GlobalDir()/release-check.json. It is machine-wide, which is why it
// is not a per-repo syncstate record. A torn read is "no state", never an error.
package releasestate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const fileName = "release-check.json"

// State is the last recorded release check.
type State struct {
	// CLILatest and ServeLatest are the newest published release tags
	// (v0.0.368, serve-v0.0.12), as the update verb resolved them.
	CLILatest   string `json:"cli_latest,omitempty"`
	ServeLatest string `json:"serve_latest,omitempty"`
	// ServeRunning is the satelled version installed when the check ran. The hook
	// cannot ask satelled without running it, so the update verb records it.
	ServeRunning string `json:"serve_running,omitempty"`
	// CheckedAt is when a check last succeeded: the cache's age.
	CheckedAt time.Time `json:"checked_at,omitempty"`
	// CheckedAttempt is when a refresh was last started (successful or not), so
	// session starts debounce the detached refresh while the network is down.
	CheckedAttempt time.Time `json:"checked_attempt,omitempty"`
}

var mu sync.Mutex

func pathFor(globalDir string) string { return filepath.Join(globalDir, fileName) }

// Load returns the recorded state. Missing or torn file → ok=false, err=nil.
func Load(globalDir string) (State, bool, error) {
	mu.Lock()
	defer mu.Unlock()
	return loadLocked(globalDir)
}

func loadLocked(globalDir string) (State, bool, error) {
	b, err := os.ReadFile(pathFor(globalDir))
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, false, nil
		}
		return State{}, false, err
	}
	var s State
	if json.Unmarshal(b, &s) != nil {
		return State{}, false, nil
	}
	return s, true, nil
}

// Record writes a completed check: the release fields and CheckedAt are
// replaced, the recorded CheckedAttempt is kept.
func Record(globalDir string, s State) error {
	mu.Lock()
	defer mu.Unlock()
	prev, _, err := loadLocked(globalDir)
	if err != nil {
		return err
	}
	s.CheckedAttempt = prev.CheckedAttempt
	return writeLocked(globalDir, s)
}

// MarkAttempt stamps that a refresh is being started at now, leaving every other
// field as it was. It is the hook's only write.
func MarkAttempt(globalDir string, now time.Time) error {
	mu.Lock()
	defer mu.Unlock()
	s, _, err := loadLocked(globalDir)
	if err != nil {
		return err
	}
	s.CheckedAttempt = now
	return writeLocked(globalDir, s)
}

func writeLocked(globalDir string, s State) error {
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dst := pathFor(globalDir)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
