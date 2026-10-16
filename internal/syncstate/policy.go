package syncstate

// Subscribed command-policy cache (sty_041bf275): the last good fetched list of
// each [[gate.command_policy]] subscription, and the session-start refresh
// attempt stamp. It is a sibling of workstate-sync.json under the same
// GlobalDir/RepoKey directory, so the gate, session start and `satelle policy
// refresh` resolve one file from any worktree of the repo. Fetched remote
// content lives here and never in the repo's data dir.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
)

const policyFileName = "command-policy.json"

// PolicySub is one subscription's recorded outcome. RulesTOML and FetchedAt are
// the last good list and move only on a successful refresh; LastError and
// LastErrorAt are the last failed one and are cleared by the next success.
type PolicySub struct {
	// Source and SourceKind (catalog or url) say where RulesTOML came from, so a
	// subscription re-pointed at another source does not inherit the old rules.
	Source      string    `json:"source"`
	SourceKind  string    `json:"source_kind"`
	FetchedAt   time.Time `json:"fetched_at,omitempty"`
	SHA256      string    `json:"sha256,omitempty"`
	RulesTOML   string    `json:"rules_toml,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitempty"`
}

// PolicyState is one repo's subscribed-list cache.
type PolicyState struct {
	// CheckedAttempt is when a session start last started a detached
	// `satelle policy refresh`. It only debounces that refresh.
	CheckedAttempt time.Time            `json:"checked_attempt,omitempty"`
	Subs           map[string]PolicySub `json:"subs,omitempty"`
}

// PolicyPath is the cache file for repoPath: the one resolver every reader and
// writer of the cache shares. RepoKey collapses a repo's worktrees, so any
// worktree names the same file.
func PolicyPath(globalDir, repoPath string) string {
	return filepath.Join(globalDir, config.RepoKey(repoPath), policyFileName)
}

// LoadPolicy returns the cache. A missing or torn file is no state, never an
// error that could fail a hook.
func LoadPolicy(globalDir, repoPath string) (PolicyState, bool, error) {
	mu.Lock()
	defer mu.Unlock()
	return loadPolicyLocked(globalDir, repoPath)
}

func loadPolicyLocked(globalDir, repoPath string) (PolicyState, bool, error) {
	b, err := os.ReadFile(PolicyPath(globalDir, repoPath))
	if err != nil {
		if os.IsNotExist(err) {
			return PolicyState{}, false, nil
		}
		return PolicyState{}, false, err
	}
	var s PolicyState
	if json.Unmarshal(b, &s) != nil {
		return PolicyState{}, false, nil
	}
	return s, true, nil
}

// RecordPolicy stores a fully validated list for name and clears its recorded
// failure. Callers pass only a body that already parsed.
func RecordPolicy(globalDir, repoPath, name string, sub PolicySub, now time.Time) error {
	return recordPolicy(globalDir, repoPath, func(s *PolicyState) {
		sub.FetchedAt, sub.LastError, sub.LastErrorAt = now, "", time.Time{}
		s.Subs[name] = sub
	})
}

// RecordPolicyFailure records why a refresh failed. The last good list and its
// fetch time are left exactly as they were.
func RecordPolicyFailure(globalDir, repoPath, name, source, kind, reason string, now time.Time) error {
	return recordPolicy(globalDir, repoPath, func(s *PolicyState) {
		sub := s.Subs[name]
		if sub.Source != source || sub.SourceKind != kind {
			// Re-pointed: the stored list is another source's, so keep none of it.
			sub = PolicySub{Source: source, SourceKind: kind}
		}
		sub.LastError, sub.LastErrorAt = reason, now
		s.Subs[name] = sub
	})
}

// MarkPolicyAttempt stamps now as the last start of a policy refresh, leaving
// every recorded list as it was.
func MarkPolicyAttempt(globalDir, repoPath string, now time.Time) error {
	return recordPolicy(globalDir, repoPath, func(s *PolicyState) { s.CheckedAttempt = now })
}

func recordPolicy(globalDir, repoPath string, merge func(*PolicyState)) error {
	mu.Lock()
	defer mu.Unlock()
	s, _, err := loadPolicyLocked(globalDir, repoPath)
	if err != nil {
		return err
	}
	if s.Subs == nil {
		s.Subs = map[string]PolicySub{}
	}
	merge(&s)
	dst := PolicyPath(globalDir, repoPath)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), policyFileName+".*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
