// Package syncstate is a leaf: durable per-repo workstate-sync outcomes
// (sty_30696eeb). serve, doctor, and the mirror UI all read/write it without
// crossing the serve→cli/hosted fences.
//
// Two field groups share one file. Each writer re-loads and merges only its
// own group (satelled and `satelle sync` are separate processes). A torn read
// is "no state", never a parse error that fails doctor.
package syncstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
)

const fileName = "workstate-sync.json"

// State is one repo's last recorded workstate-sync outcome.
type State struct {
	RepoPath string `json:"repo_path"`
	Scope    string `json:"scope,omitempty"`

	PushLastSuccess time.Time `json:"push_last_success,omitempty"`
	PushLastFailure time.Time `json:"push_last_failure,omitempty"`
	PushReason      string    `json:"push_reason,omitempty"`
	PushPasses      int       `json:"push_passes,omitempty"`

	SnapshotLastSuccess time.Time `json:"snapshot_last_success,omitempty"`
	SnapshotLastFailure time.Time `json:"snapshot_last_failure,omitempty"`
	SnapshotReason      string    `json:"snapshot_reason,omitempty"`
	SnapshotPasses      int       `json:"snapshot_passes,omitempty"`
}

var mu sync.Mutex

func pathFor(globalDir, repoPath string) string {
	return filepath.Join(globalDir, config.RepoKey(repoPath), fileName)
}

// Load returns the recorded state. Missing or torn file → ok=false, err=nil.
func Load(globalDir, repoPath string) (State, bool, error) {
	mu.Lock()
	defer mu.Unlock()
	return loadLocked(globalDir, repoPath)
}

func loadLocked(globalDir, repoPath string) (State, bool, error) {
	b, err := os.ReadFile(pathFor(globalDir, repoPath))
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

// RecordPush merges the push-leg fields and writes atomically.
func RecordPush(globalDir, repoPath string, success bool, reason, scope string, now time.Time) error {
	return record(globalDir, repoPath, scope, func(s *State) {
		if success {
			s.PushLastSuccess = now
			s.PushReason = ""
			s.PushPasses = 0
			return
		}
		s.PushLastFailure = now
		if reason != "" && reason == s.PushReason {
			s.PushPasses++
		} else {
			s.PushReason = reason
			s.PushPasses = 1
		}
	})
}

// RecordSnapshot merges the snapshot-leg fields and writes atomically.
func RecordSnapshot(globalDir, repoPath string, success bool, reason, scope string, now time.Time) error {
	return record(globalDir, repoPath, scope, func(s *State) {
		if success {
			s.SnapshotLastSuccess = now
			s.SnapshotReason = ""
			s.SnapshotPasses = 0
			return
		}
		s.SnapshotLastFailure = now
		if reason != "" && reason == s.SnapshotReason {
			s.SnapshotPasses++
		} else {
			s.SnapshotReason = reason
			s.SnapshotPasses = 1
		}
	})
}

func record(globalDir, repoPath, scope string, merge func(*State)) error {
	mu.Lock()
	defer mu.Unlock()
	s, _, err := loadLocked(globalDir, repoPath)
	if err != nil {
		return err
	}
	s.RepoPath = strings.TrimSpace(repoPath)
	if scope != "" {
		s.Scope = scope
	}
	merge(&s)
	return writeLocked(globalDir, repoPath, s)
}

func writeLocked(globalDir, repoPath string, s State) error {
	dir := filepath.Join(globalDir, config.RepoKey(repoPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dst := pathFor(globalDir, repoPath)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
