package config

// [sync] scope resolution — the foundation config layer for epic:scoped-sync
// (sty_2ff2232d). This story is PURE CONFIG: no network, no git side-effects.
// The scope ladder decides whether a .satelle area's authored files stay on
// this machine (local, the default), sync to one operator's own machines
// (personal), or sync to the whole team (shared). A future `satelle sync`
// engine consumes ScopeFor/FileShared to decide what to push/pull; it is not
// part of this story.

import (
	"fmt"
	"strings"
)

// Scope is a position on the local -> personal -> shared sync ladder for a
// .satelle authored area.
type Scope int

const (
	// LocalScope never leaves this machine. The default for any area not
	// explicitly configured — nothing syncs without opt-in.
	LocalScope Scope = iota
	// PersonalScope syncs to the same operator's other machines.
	PersonalScope
	// SharedScope syncs to the whole team.
	SharedScope
)

// String renders the scope as its [sync] config value.
func (s Scope) String() string {
	switch s {
	case PersonalScope:
		return "personal"
	case SharedScope:
		return "shared"
	default:
		return "local"
	}
}

// ParseScope parses a [sync] area value off the local|personal|shared ladder.
// An unrecognised value is a config error (satelle's fail-fast posture) rather
// than a silent fallback to local — a typo in an EXPLICITLY set area must not
// go unnoticed.
func ParseScope(s string) (Scope, error) {
	switch s {
	case "local":
		return LocalScope, nil
	case "personal":
		return PersonalScope, nil
	case "shared":
		return SharedScope, nil
	default:
		return LocalScope, fmt.Errorf("sync: unknown scope %q (want local|personal|shared)", s)
	}
}

// SyncAreas is the canonical, de-duplicated list of .satelle area names a
// [sync] table may configure: the AuthoredKinds (documents/workflows/
// principles/skills) plus the remaining epic-named areas — the project
// constitution, the agents layer, tasks, and the work-state areas (stories,
// ledger, executions).
var SyncAreas = buildSyncAreas()

func buildSyncAreas() []string {
	out := make([]string, 0, len(AuthoredKinds)+6)
	out = append(out, AuthoredKinds...)
	return append(out, "constitution", "agents", "tasks", "stories", "ledger", "executions")
}

// ScopeFor resolves a configured area's scope. An area absent from [sync] (or
// present but blank) resolves to LocalScope. An area explicitly set to a value
// outside local|personal|shared is a config error, never a silent local.
func ScopeFor(cfg Config, area string) (Scope, error) {
	raw, ok := cfg.Sync[area]
	if !ok || strings.TrimSpace(raw) == "" {
		return LocalScope, nil
	}
	return ParseScope(raw)
}

// sharedFrontmatterKey is the OKF frontmatter key marking an individual file,
// inside a personal-scope area, for promotion to the shared tier.
const sharedFrontmatterKey = "shared"

// FileShared reports whether a file's frontmatter marks it shared. frontmatter
// is the file's raw body (or just its leading `---`-fenced YAML block) — either
// works, since only the leading fence is scanned. The flag is only meaningful
// inside a personal-scope area (AC2): local and shared areas apply uniformly to
// every file they hold, so any other scope reports false without inspecting
// frontmatter at all.
func FileShared(scope Scope, frontmatter string) bool {
	if scope != PersonalScope {
		return false
	}
	return fmBoolScalar(frontmatter, sharedFrontmatterKey)
}

// fmBoolScalar extracts a top-level YAML boolean scalar named key from a
// `---`-fenced frontmatter block (or a full markdown body carrying one).
// Missing, malformed, or non-"true" values fail closed to false.
func fmBoolScalar(body, key string) bool {
	lines := strings.Split(body, "\n")
	start := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		start = 1
	}
	prefix := key + ":"
	for i := start; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "---" {
			break
		}
		if t == prefix || strings.HasPrefix(t, prefix+" ") {
			v := strings.TrimSpace(strings.TrimPrefix(t, prefix))
			v = strings.Trim(v, `"'`)
			return v == "true"
		}
	}
	return false
}
