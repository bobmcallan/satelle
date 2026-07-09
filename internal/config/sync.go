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
	"os"
	"path/filepath"
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

// ConfigAreas are the .satelle areas eligible for the versioned config store
// (epic:scoped-sync, order:5): the authored kinds MINUS documents (which is its
// own sync kind), plus the project constitution, the agents layer, and task
// DEFINITIONS. The work-state areas (stories/ledger/executions) are order:7;
// documents is its own kind. Local-scope areas are skipped at push time — this
// list is the candidate set the walk resolves a tier for.
var ConfigAreas = []string{"workflows", "principles", "skills", "constitution", "agents", "tasks"}

// ConfigTier is a config file's resolved sync DESTINATION: the caller's own
// workspace (PersonalTier) or the team workspace (SharedTier). A LocalScope area
// never reaches tier resolution — it is skipped wholesale before any file is
// read, so there is no LocalTier.
type ConfigTier int

const (
	// PersonalTier routes to the caller's own workspace (per-user isolated).
	PersonalTier ConfigTier = iota
	// SharedTier routes to the team workspace (the shared home — "set up X like Y").
	SharedTier
)

// String renders the tier as its push/deploy label.
func (t ConfigTier) String() string {
	if t == SharedTier {
		return "shared"
	}
	return "personal"
}

// ConfigFile is one resolved authored-config file destined for the versioned
// store. Path is server-relative (forward slashes, no leading ".satelle/", e.g.
// "skills/my-skill.md", "agents.toml", "constitution.md", "tasks/tsk_x.md") —
// the key the server stores the file under, stable regardless of where a kind
// lives on disk via [substrate_roots].
type ConfigFile struct {
	Area    string     // the config-area name (skills, constitution, agents, tasks, ...)
	Path    string     // server-relative path under the workspace-config root
	Tier    ConfigTier // resolved destination (PersonalTier | SharedTier)
	Content []byte     // verbatim file bytes
}

// ConfigFiles walks the ConfigAreas under repoRoot, resolving each file's scope
// via ScopeFor and partitioning the non-local files into personal/shared tiers.
// A shared-scope area is SharedTier wholesale; a personal-scope area is
// PersonalTier per file, PROMOTED to SharedTier when the file is markdown whose
// frontmatter marks it shared (FileShared — the per-file shared flag). A
// local-scope area contributes nothing (AC1: skip scope=local). Reserved
// generated views (index.md/log.md/README) are excluded — they are not authored.
// Files are returned sorted by Path. A non-existent area on disk is benign
// (nothing to push yet). An explicitly invalid scope is a hard error.
func ConfigFiles(cfg Config, repoRoot string) ([]ConfigFile, error) {
	var out []ConfigFile
	for _, area := range ConfigAreas {
		scope, err := ScopeFor(cfg, area)
		if err != nil {
			return nil, fmt.Errorf("sync config area %q: %w", area, err)
		}
		if scope == LocalScope {
			continue
		}
		location, isDir := ConfigAreaLocation(cfg, repoRoot, area)
		if location == "" {
			continue
		}
		if !isDir {
			serverPath := filepath.Base(location)
			if cf, ok, err := readConfigFile(area, location, serverPath, scope); err != nil {
				return nil, err
			} else if ok {
				out = append(out, cf)
			}
			continue
		}
		walkErr := filepath.WalkDir(location, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) && p == location {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			if isReservedView(filepath.Base(p)) {
				return nil
			}
			rel, rerr := filepath.Rel(location, p)
			if rerr != nil {
				return rerr
			}
			serverPath := area + "/" + filepath.ToSlash(rel)
			cf, ok, rerr := readConfigFile(area, p, serverPath, scope)
			if rerr != nil {
				return rerr
			}
			if ok {
				out = append(out, cf)
			}
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("sync config area %q: %w", area, walkErr)
		}
	}
	sortConfigFiles(out)
	return out, nil
}

// readConfigFile reads one file's bytes and resolves its tier. serverPath is the
// already-computed server-relative key; absPath is the on-disk source. A missing
// single-file area (constitution/agents not yet seeded) is benign — ok=false.
func readConfigFile(area, absPath, serverPath string, scope Scope) (ConfigFile, bool, error) {
	body, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ConfigFile{}, false, nil
		}
		return ConfigFile{}, false, fmt.Errorf("read %s: %w", absPath, err)
	}
	tier := PersonalTier
	if scope == SharedScope {
		tier = SharedTier
	} else if scope == PersonalScope && FileShared(PersonalScope, string(body)) {
		tier = SharedTier
	}
	return ConfigFile{Area: area, Path: serverPath, Tier: tier, Content: body}, true, nil
}

// sortConfigFiles orders files by server Path for a deterministic push.
func sortConfigFiles(files []ConfigFile) {
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j-1].Path > files[j].Path; j-- {
			files[j-1], files[j] = files[j], files[j-1]
		}
	}
}

// isReservedView reports whether a file basename is a generated read-only view
// (index.md, log.md) or a non-substrate README — never authored, so never part
// of a config push. Shared with the `sync scopes` shared-file scan.
func isReservedView(name string) bool {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	return base == "index" || base == "log" || strings.EqualFold(base, "README")
}

// ConfigAreaLocation resolves a ConfigArea's on-disk location. isDir reports
// whether location is a directory to walk (vs. a single file to read directly).
// It mirrors the cli syncAreaPath resolution but lives in config so the walk is
// unit-testable without the local store.
func ConfigAreaLocation(cfg Config, repoRoot, area string) (location string, isDir bool) {
	dataDir := cfg.ResolveDataDir(repoRoot)
	switch area {
	case "constitution":
		return cfg.ResolveConstitution(repoRoot), false
	case "agents":
		return filepath.Join(dataDir, AgentsConfigName), false
	case "tasks":
		return filepath.Join(dataDir, "tasks"), true
	default:
		if dir := cfg.ResolveAuthoredDirs(repoRoot)[area]; dir != "" {
			return dir, true
		}
		return "", false
	}
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
