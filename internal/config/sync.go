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
	"sort"
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
// ledger, executions). The reserved key syncAllKey ("all") is NOT a member —
// it only defaults areas that fall through unset (see ScopeFor).
var SyncAreas = buildSyncAreas()

// syncAllKey is the reserved [sync] key that blanket-defaults every area not
// explicitly set: one line — [sync] all = "personal" — opts the whole .satelle
// tree into personal sync instead of listing each area (epic:sync-publish). It
// is not itself a walk area (absent from SyncAreas); it only supplies the
// fallthrough scope in ScopeFor.
const syncAllKey = "all"

func buildSyncAreas() []string {
	out := make([]string, 0, len(AuthoredKinds)+6)
	out = append(out, AuthoredKinds...)
	return append(out, "constitution", "agents", "tasks", "stories", "ledger", "executions")
}

// ScopeFor resolves a configured area's scope. Precedence: an explicit
// [sync] <area> value wins; else the blanket [sync] all fallthrough; else
// LocalScope. A value outside local|personal|shared is a config error, never a
// silent local — for the per-area key when it is set, and for all only when an
// area actually falls through to it (a bad all beside a valid override does not
// error the overridden area).
func ScopeFor(cfg Config, area string) (Scope, error) {
	if raw, ok := cfg.Sync[area]; ok && strings.TrimSpace(raw) != "" {
		return ParseScope(raw)
	}
	if raw, ok := cfg.Sync[syncAllKey]; ok && strings.TrimSpace(raw) != "" {
		scope, err := ParseScope(strings.TrimSpace(raw))
		if err != nil {
			return LocalScope, fmt.Errorf("sync: [sync] all = %q is not local|personal|shared", strings.TrimSpace(raw))
		}
		return scope, nil
	}
	return LocalScope, nil
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

// ConfigTier flags whether a file is eligible to be exposed via satelle publish
// (SharedTier) or stays purely personal (PersonalTier). It is NOT a sync push
// destination — satelle sync always writes to the personal workspace; Tier ==
// SharedTier only triggers an operator note (epic:sync-publish). A LocalScope
// area never reaches tier resolution — it is skipped wholesale before any file
// is read, so there is no LocalTier.
type ConfigTier int

const (
	// PersonalTier is ordinary personal-scope content (sync destination: personal).
	PersonalTier ConfigTier = iota
	// SharedTier marks [sync] area = shared or frontmatter shared:true. Sync still
	// pushes personal-only; the CLI emits a note pointing operators at satelle
	// publish for team catalog exposure (not dual-destination).
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
	Tier    ConfigTier // PersonalTier | SharedTier (label only; sync dest is personal)
	Content []byte     // verbatim file bytes
}

// Bundle is an assembled set of files destined for transmission, plus the
// server-relative paths withheld by LocalOnlyPath so a caller can REPORT them.
// Work-state push transmits DB rows (no on-disk filename), so it is out of
// scope for this exclusion by deliberate decision — not a gap.
type Bundle struct {
	Files    []ConfigFile
	Withheld []string // sorted, server-relative
}

// LocalOnlyPath reports whether a path is one that must NEVER be transmitted:
// any path segment carrying a `.local` dot-component (satelle.local.toml,
// notes.local.md, secrets.local/keys.md). Unconditional — it takes no Config,
// so no setting can disable or narrow it. The restore-side counterpart is
// subsync.ExcludedLocal (which delegates here for the .local case).
//
// A bare segment named "local" (skills/local/x.md) is an ordinary name and is
// not withheld; the match requires a multi-component segment whose non-stem
// component equals "local" (case-insensitive).
func LocalOnlyPath(p string) bool {
	p = filepath.ToSlash(p)
	for _, seg := range strings.Split(p, "/") {
		if seg == "" {
			continue
		}
		parts := strings.Split(seg, ".")
		if len(parts) < 2 {
			continue // no dots → ordinary name (e.g. "local")
		}
		// Non-stem components only: local.md stays free; notes.local.md is withheld.
		for _, c := range parts[1:] {
			if strings.EqualFold(c, "local") {
				return true
			}
		}
	}
	return false
}

// assemble is the single seam every push bundle passes through: it applies the
// .local exclusion and the deterministic sort. Per-area walks never filter —
// so a new area added to ConfigAreas inherits the guarantee automatically.
func assemble(files []ConfigFile) Bundle {
	var kept []ConfigFile
	var withheld []string
	for _, f := range files {
		if LocalOnlyPath(f.Path) {
			withheld = append(withheld, f.Path)
			continue
		}
		kept = append(kept, f)
	}
	sortConfigFiles(kept)
	sort.Strings(withheld)
	return Bundle{Files: kept, Withheld: withheld}
}

// ConfigFiles walks the ConfigAreas under repoRoot, resolving each file's scope
// via ScopeFor. Local-scope areas contribute nothing. Non-local files are labeled
// PersonalTier or SharedTier for operator messaging / publish eligibility — both
// still sync to the personal workspace only (epic:sync-publish). A shared-scope
// area is SharedTier wholesale; a personal-scope area is PersonalTier per file,
// promoted to SharedTier when FileShared (frontmatter shared:true). Reserved
// generated views (index.md/log.md/README) are excluded — they are not authored.
// Paths matching LocalOnlyPath are withheld (listed in Bundle.Withheld, never in
// Files). Files are returned sorted by Path. A non-existent area on disk is benign
// (nothing to push yet). An explicitly invalid scope is a hard error.
func ConfigFiles(cfg Config, repoRoot string) (Bundle, error) {
	var out []ConfigFile
	for _, area := range ConfigAreas {
		files, _, err := filesForArea(cfg, repoRoot, area)
		if err != nil {
			return Bundle{}, err
		}
		out = append(out, files...)
	}
	return assemble(out), nil
}

// DocumentFiles walks the documents area under repoRoot with the same scope/
// tier labeling as ConfigFiles (local skip; SharedTier marks publish eligibility
// only — sync destination remains personal). The returned Scope is the area's
// resolved scope so the CLI can print a clear "scope=local, skipping" message
// instead of an ambiguous empty list. Documents is its own sync kind (order:6)
// — not part of ConfigAreas. Paths matching LocalOnlyPath are withheld.
func DocumentFiles(cfg Config, repoRoot string) (Bundle, Scope, error) {
	files, scope, err := filesForArea(cfg, repoRoot, "documents")
	if err != nil {
		return Bundle{}, scope, err
	}
	return assemble(files), scope, nil
}

// filesForArea walks one named area under repoRoot, applying ScopeFor + tier
// resolution. A local-scope area returns (nil, LocalScope, nil). The returned
// Scope is always the resolved value so callers can distinguish "local, skip"
// from "personal/shared with no files on disk yet".
func filesForArea(cfg Config, repoRoot, area string) ([]ConfigFile, Scope, error) {
	scope, err := ScopeFor(cfg, area)
	if err != nil {
		return nil, LocalScope, fmt.Errorf("sync area %q: %w", area, err)
	}
	if scope == LocalScope {
		return nil, scope, nil
	}
	location, isDir := ConfigAreaLocation(cfg, repoRoot, area)
	if location == "" {
		// documents (and other authored dirs) resolve via ResolveAuthoredDirs;
		// constitution/agents use dedicated paths. An area with no location is
		// not a config/document candidate on disk.
		return nil, scope, nil
	}
	var out []ConfigFile
	if !isDir {
		serverPath := filepath.Base(location)
		if cf, ok, err := readConfigFile(area, location, serverPath, scope); err != nil {
			return nil, scope, err
		} else if ok {
			out = append(out, cf)
		}
		return out, scope, nil
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
		return nil, scope, fmt.Errorf("sync area %q: %w", area, walkErr)
	}
	return out, scope, nil
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

// ConfigAreaLocation resolves a config or documents area's on-disk location.
// isDir reports whether location is a directory to walk (vs. a single file to
// read directly). It mirrors the cli syncAreaPath resolution but lives in
// config so the walk is unit-testable without the local store. documents is
// resolved here too so DocumentFiles can reuse the same path helper.
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
		// AuthoredKinds (documents/workflows/principles/skills) + any other
		// directory-shaped area under dataDir.
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
