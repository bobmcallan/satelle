// Package syncstate is a leaf: durable per-repo workstate-sync outcomes
// (sty_30696eeb). serve, doctor, and the mirror UI all read/write it without
// crossing the serve→cli/hosted fences.
//
// Five field groups share one file (push, snapshot, substrate areas, the
// substrate refresh attempt stamp, and story conflicts). Each writer re-loads and merges only its
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
	// Scope is the sync scope the last resolve found (local|personal|team) and
	// Binding the [sync] project it resolved for. The offline checks read them
	// through RecordedScope; a resolve is the only writer.
	Scope   string    `json:"scope,omitempty"`
	Binding string    `json:"binding,omitempty"`
	ScopeAt time.Time `json:"scope_at,omitempty"`

	PushLastSuccess time.Time `json:"push_last_success,omitempty"`
	PushLastFailure time.Time `json:"push_last_failure,omitempty"`
	PushReason      string    `json:"push_reason,omitempty"`
	PushPasses      int       `json:"push_passes,omitempty"`

	SnapshotLastSuccess time.Time `json:"snapshot_last_success,omitempty"`
	SnapshotLastFailure time.Time `json:"snapshot_last_failure,omitempty"`
	SnapshotReason      string    `json:"snapshot_reason,omitempty"`
	SnapshotPasses      int       `json:"snapshot_passes,omitempty"`

	// SubstrateAreas is the last verified state of each substrate area against
	// the hosted copy (sty_7562055c). The states are the words snapsync.Status
	// produces, recorded by whoever just asked it — never assumed from an
	// operation having succeeded. Which paths are unmerged is NOT kept here: the
	// area's synced base owns that.
	SubstrateAreas map[string]SubstrateArea `json:"substrate_areas,omitempty"`

	// SubstrateCheckedAttempt is when a session start last started a detached
	// `satelle sync status` to refresh SubstrateAreas. It only debounces that
	// refresh; it says nothing about the areas' state.
	SubstrateCheckedAttempt time.Time `json:"substrate_checked_attempt,omitempty"`
	// StoryConflicts are the stories a sync pass did not apply because the local
	// and hosted copies differ in content (sty_6e5fc41c). A pass records the
	// disagreement and never resolves it; a later pass that applies the story, or
	// finds both copies identical, clears it.
	StoryConflicts map[string]StoryConflict `json:"story_conflicts,omitempty"`
}

// StoryConflict is one story the local and hosted copies disagree on: why the
// pass left it, and when.
type StoryConflict struct {
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// SubstrateArea is one area's recorded state and when it was verified.
type SubstrateArea struct {
	State     string    `json:"state"`
	CheckedAt time.Time `json:"checked_at"`
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

// RecordScope records the scope a resolve found for binding, leaving every
// other field as it was. A resolve that finds what is already recorded writes
// nothing, so a read-only command leaves the state as it was; ScopeAt is when
// the recorded scope was first resolved.
func RecordScope(globalDir, repoPath string, scope config.Scope, binding string, now time.Time) error {
	binding = strings.TrimSpace(binding)
	mu.Lock()
	s, ok, err := loadLocked(globalDir, repoPath)
	mu.Unlock()
	if err == nil && ok && s.Scope == scope.String() && s.Binding == binding && !s.ScopeAt.IsZero() {
		return nil
	}
	return record(globalDir, repoPath, scope.String(), func(s *State) {
		s.Binding = binding
		s.ScopeAt = now
	})
}

// RecordedScope is the scope the offline checks judge sync from: the last
// resolve's, when it was made for the current binding. An empty binding, no
// record, or a record for another binding (a rebind since) is local.
func RecordedScope(globalDir, repoPath, binding string) config.Scope {
	binding = strings.TrimSpace(binding)
	if binding == "" {
		return config.LocalScope
	}
	s, ok, err := Load(globalDir, repoPath)
	if err != nil || !ok || s.Binding != binding {
		return config.LocalScope
	}
	scope, err := config.ParseScope(s.Scope)
	if err != nil {
		return config.LocalScope
	}
	return scope
}

// RecordSubstrate merges the verified state of the given areas (area -> state)
// into the substrate group, leaving every other area's record as it was.
func RecordSubstrate(globalDir, repoPath string, areas map[string]string, now time.Time) error {
	if len(areas) == 0 {
		return nil
	}
	return record(globalDir, repoPath, "", func(s *State) {
		if s.SubstrateAreas == nil {
			s.SubstrateAreas = map[string]SubstrateArea{}
		}
		for area, state := range areas {
			s.SubstrateAreas[area] = SubstrateArea{State: state, CheckedAt: now}
		}
	})
}

// MarkSubstrateAttempt stamps now as the last start of a substrate refresh,
// leaving every other field as it was.
func MarkSubstrateAttempt(globalDir, repoPath string, now time.Time) error {
	return record(globalDir, repoPath, "", func(s *State) { s.SubstrateCheckedAttempt = now })
}

// RecordStoryConflicts merges one pass's findings into the story-conflict group:
// conflicts (id -> reason) are recorded, cleared ids are dropped, and every other
// story's record is left as it was. Nothing is written when there is nothing to
// change, so an in-step repo gains no state file.
func RecordStoryConflicts(globalDir, repoPath string, conflicts map[string]string, cleared []string, now time.Time) error {
	if len(conflicts) == 0 && len(cleared) == 0 {
		return nil
	}
	if len(conflicts) == 0 {
		s, ok, err := Load(globalDir, repoPath)
		if err != nil || !ok || len(s.StoryConflicts) == 0 {
			return err
		}
	}
	return record(globalDir, repoPath, "", func(s *State) {
		for _, id := range cleared {
			delete(s.StoryConflicts, id)
		}
		for id, reason := range conflicts {
			if s.StoryConflicts == nil {
				s.StoryConflicts = map[string]StoryConflict{}
			}
			// A story still in conflict keeps the time it was first found.
			if prev, ok := s.StoryConflicts[id]; ok && prev.Reason == reason {
				continue
			}
			s.StoryConflicts[id] = StoryConflict{Reason: reason, At: now}
		}
		if len(s.StoryConflicts) == 0 {
			s.StoryConflicts = nil
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
