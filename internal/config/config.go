// Package config is satelle's trimmed TOML configuration, ported from
// satellites' internal/cliconfig with the server/token/credstore surface
// dropped (the OSS tier is always local — no remote dispatch). What remains:
//
//   - Repo-root resolution: walk up from CWD for .satelle/satelle.toml.
//   - Defaults for every setting — a repo with no satelle.toml runs zero-config.
//   - A gitignored satelle.local.toml overlay beside the committed config.
//   - [substrate_roots]: per-kind authored-markdown dirs, which MAY live outside
//     .satelle/ (the directory monitor watches whatever these point at).
//   - data_dir / db: where the per-repo sqlite database lives.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Defaults applied when a key is unset. Every setting has one, so a repo with
// an empty (or absent) satelle.toml runs on defaults for all keys.
const (
	// DefaultDataDir is the per-repo home for satelle's data stores —
	// <repo>/.satelle. A relative data_dir resolves under the repo root.
	DefaultDataDir = ".satelle"
	// DefaultDBName is the sqlite database file inside data_dir.
	DefaultDBName = "satelle.db"
	// DefaultConstitutionName is the project constitution file at the data-dir
	// root (.satelle/constitution.md) — the order-zero doc injected every session
	// (epic:session-context). It lives OUTSIDE the authored-kind dirs, so it is
	// read directly, not indexed as a kind.
	DefaultConstitutionName = "constitution.md"
	// DefaultWebPort is the local web server's listen port.
	DefaultWebPort = 8787
	// DefaultLogLevel is arbor's level when log_level is unset.
	DefaultLogLevel = "info"
	// DefaultLogsMaxSizeKB caps each flat evidence log (.satelle/logs/*.log) before
	// it rolls; DefaultLogsMaxFiles bounds how many rotations are kept. Daily
	// rolling is always on regardless of these (sty_a67e6e8c).
	DefaultLogsMaxSizeKB = 5120 // 5 MiB
	DefaultLogsMaxFiles  = 7
	// ConfigName / LocalConfigName are the committed config and its gitignored
	// per-user overlay, both under <repo>/.satelle/.
	ConfigName      = "satelle.toml"
	LocalConfigName = "satelle.local.toml"
)

// AuthoredKinds are the markdown-source-of-truth artifact kinds the directory
// monitor watches and indexes. Each defaults to <repo>/.satelle/<kind> and is
// individually relocatable via [substrate_roots] (may be outside .satelle/).
var AuthoredKinds = []string{"documents", "workflows", "principles", "skills"}

// Config is the on-disk shape at .satelle/satelle.toml. Every field is
// optional; the Resolve* methods supply defaults so the zero value is valid.
type Config struct {
	// DataDir is the per-repo store home; empty means DefaultDataDir. A
	// relative value resolves under the repo root, never the process CWD.
	DataDir string `toml:"data_dir"`
	// DB overrides the database path; empty means <data_dir>/satelle.db. A
	// relative value resolves under the repo root.
	DB string `toml:"db"`
	// SubstrateRoots maps an authored kind to the parent dir holding it. UNSET
	// for a kind means the default <data_dir>/<kind>. An absolute value lets a
	// kind's source live anywhere on disk.
	SubstrateRoots map[string]string `toml:"substrate_roots"`
	// WebPort is the local web server port; zero means DefaultWebPort.
	WebPort int `toml:"web_port"`
	// LogLevel is arbor's level (debug|info|warn|error); empty means info.
	LogLevel string `toml:"log_level"`
	// LogsMaxSizeKB caps each flat evidence log under .satelle/logs before it rolls;
	// zero means DefaultLogsMaxSizeKB. LogsMaxFiles bounds kept rotations; zero means
	// DefaultLogsMaxFiles. Daily rolling is always on.
	LogsMaxSizeKB int `toml:"logs_max_size_kb"`
	LogsMaxFiles  int `toml:"logs_max_files"`
	// StoriesKeepClosed keeps the N most-recently-updated CLOSED-story attachment
	// dirs under .satelle/stories; 0 (default) disables count-based pruning.
	// StoriesKeepDays prunes a closed story's dir when the story's terminal update
	// is older than N days; 0 (default) disables age-based pruning. The two
	// compose — either triggers pruning. A NON-terminal story's dir is ALWAYS kept
	// regardless of either setting. Pruning MOVES the dir to
	// .satelle/backups/stories/ (never deletes in place). (sty_aba7200c)
	StoriesKeepClosed int `toml:"stories_keep_closed"`
	StoriesKeepDays   int `toml:"stories_keep_days"`
	// Review opts this repo into reviewer-gated work. Off by default — the
	// rubrics ship embedded, but ENFORCEMENT is the operator's choice (the
	// process is configured, not hardcoded-on).
	Review ReviewConfig `toml:"review"`
	// Gate tunes the PreToolUse edit gate (the `satelle hook gate` handler).
	Gate GateConfig `toml:"gate"`
	// Hosted records the hosted-server binding for `satelle login` — the server
	// URL and the project slug. Both are committed (secret-free); the OAuth
	// access/refresh TOKENS are NEVER stored here — they live in the user-level
	// credential store outside the repo (see internal/hosted). (sty_2fc93374)
	Hosted HostedConfig `toml:"hosted"`
	// Vars is the operator KV an agents.toml binding's env values substitute via
	// ${NAME} (sty_001558ce) — how a dispatched step points at an alternate model
	// backend (e.g. GLM's Anthropic-compatible endpoint) without a wrapper binary.
	// NON-secret vars MAY live in the committed satelle.toml; SECRETS (API keys)
	// go in the gitignored satelle.local.toml overlay, whose keys win per-key. The
	// KV is file-only — never persisted to the DB and never included in a substrate
	// push (satelle.local.toml is excluded), so secrets do not leave the machine.
	Vars map[string]string `toml:"vars"`
	// Sync maps a .satelle area name (see SyncAreas) to its scope on the
	// local|personal|shared ladder (epic:scoped-sync). Unset means "local" —
	// nothing syncs without explicit opt-in. A committed [sync] table sets team
	// defaults; satelle.local.toml overrides per-area for a single developer, the
	// same per-key overlay merge as Vars (sty_a2d2e057).
	Sync map[string]string `toml:"sync"`
}

// HostedConfig binds a repo to a hosted satelle-server. Secret-free and
// committed in satelle.toml; the login tokens are stored per-user outside the
// repo (internal/hosted credential store), never here.
type HostedConfig struct {
	// Server is the hosted-server base URL (e.g. https://hosted.satelle.dev).
	Server string `toml:"server"`
	// Project is the hosted project slug this repo maps to (optional).
	Project string `toml:"project"`
}

// ReviewConfig toggles the quality-management gates for a repo.
type ReviewConfig struct {
	// GateCreate runs the required-structure reviewer on story/task creation,
	// pushing non-conforming drafts back instead of persisting them.
	GateCreate bool `toml:"gate_create"`
}

// GateConfig tunes the PreToolUse edit gate. EditExemptPaths lists repo-relative
// (or absolute) path prefixes whose edits are exempt from the engaged-story gate,
// IN ADDITION to the always-exempt data dir. Empty by default so the binary stays
// CLI-vendor-neutral — a repo opts a harness authoring dir (e.g. ".claude/", which
// holds authored skills, not product code) in as configuration, never a Go rule
// (satelle-repo-agnostic / the constitution).
type GateConfig struct {
	EditExemptPaths []string `toml:"edit_exempt_paths"`
}

// ErrNotFound signals no satelle.toml was found walking up from CWD. Callers
// fall back to the zero-value Config (zero-config still works).
var ErrNotFound = errors.New("config: not found")

// resolveUnder joins a possibly-relative path against repoRoot. Absolute paths
// pass through; an empty repoRoot falls back to ".".
func resolveUnder(repoRoot, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if strings.TrimSpace(repoRoot) == "" {
		repoRoot = "."
	}
	return filepath.Join(repoRoot, p)
}

// ResolveDataDir returns the absolute data dir for repoRoot.
func (c Config) ResolveDataDir(repoRoot string) string {
	p := strings.TrimSpace(c.DataDir)
	if p == "" {
		p = DefaultDataDir
	}
	return resolveUnder(repoRoot, p)
}

// ResolveEditExemptPaths returns the configured [gate] edit_exempt_paths as
// absolute prefixes under repoRoot. Blank entries are DROPPED (a blank prefix
// would classify every edit as exempt and silently disable the gate — see
// withinRoot's fail-open-toward-inside default), and an absolute entry passes
// through unchanged. Returns nil when nothing is configured, so the default
// binary exempts only the data dir.
func (c Config) ResolveEditExemptPaths(repoRoot string) []string {
	if len(c.Gate.EditExemptPaths) == 0 {
		return nil
	}
	out := make([]string, 0, len(c.Gate.EditExemptPaths))
	for _, p := range c.Gate.EditExemptPaths {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, resolveUnder(repoRoot, s))
		}
	}
	return out
}

// ResolveConstitution returns the absolute path to the repo's project
// constitution — <data_dir>/constitution.md — the order-zero doc injected every
// session (epic:session-context). Read directly (it is not an indexed kind).
func (c Config) ResolveConstitution(repoRoot string) string {
	return filepath.Join(c.ResolveDataDir(repoRoot), DefaultConstitutionName)
}

// ResolveDB returns the absolute sqlite database path. An explicit db wins;
// otherwise <data_dir>/satelle.db.
func (c Config) ResolveDB(repoRoot string) string {
	if p := strings.TrimSpace(c.DB); p != "" {
		return resolveUnder(repoRoot, p)
	}
	return filepath.Join(c.ResolveDataDir(repoRoot), DefaultDBName)
}

// ResolveWebPort returns the web port, defaulting when unset.
func (c Config) ResolveWebPort() int {
	if c.WebPort > 0 {
		return c.WebPort
	}
	return DefaultWebPort
}

// ResolveLogsMaxSizeBytes returns the per-file size cap (bytes) for the flat
// evidence logs under .satelle/logs, before a file rolls.
func (c Config) ResolveLogsMaxSizeBytes() int64 {
	kb := c.LogsMaxSizeKB
	if kb <= 0 {
		kb = DefaultLogsMaxSizeKB
	}
	return int64(kb) * 1024
}

// ResolveLogsMaxFiles returns how many rotated flat-log files to keep per log.
func (c Config) ResolveLogsMaxFiles() int {
	if c.LogsMaxFiles > 0 {
		return c.LogsMaxFiles
	}
	return DefaultLogsMaxFiles
}

// ResolveLogLevel returns the log level, defaulting empty to info.
func (c Config) ResolveLogLevel() string {
	if s := strings.TrimSpace(c.LogLevel); s != "" {
		return s
	}
	return DefaultLogLevel
}

// ResolveAuthoredDirs returns kind→absolute-dir for every AuthoredKind, with
// [substrate_roots] overrides applied over the <data_dir>/<kind> default. An
// override may be absolute, placing a kind's source anywhere on disk.
func (c Config) ResolveAuthoredDirs(repoRoot string) map[string]string {
	out := make(map[string]string, len(AuthoredKinds))
	dataDir := c.ResolveDataDir(repoRoot)
	for _, kind := range AuthoredKinds {
		if override := strings.TrimSpace(c.SubstrateRoots[kind]); override != "" {
			// substrate_roots names the PARENT dir; the kind's files live in
			// <override>/<kind>, mirroring satellites' [substrate_roots] semantics.
			out[kind] = resolveUnder(repoRoot, filepath.Join(override, kind))
			continue
		}
		out[kind] = filepath.Join(dataDir, kind)
	}
	return out
}

// RepoRootFromConfigPath derives the repo root (the dir holding .satelle/) from
// a <repo>/.satelle/satelle.toml path. Empty path → "." (CWD).
func RepoRootFromConfigPath(configPath string) string {
	if strings.TrimSpace(configPath) == "" {
		return "."
	}
	return filepath.Dir(filepath.Dir(configPath))
}

// Load resolves and parses the config, applying the satelle.local.toml overlay.
// It returns the Config, the resolved committed-config path (for repo-root
// derivation), and any error. A missing config is ErrNotFound — callers may
// treat that as "use the zero-value Config" for zero-config operation.
func Load(explicitPath string) (Config, string, error) {
	path, err := resolvePath(explicitPath)
	if err != nil {
		return Config{}, "", err
	}
	if path == "" {
		return Config{}, "", ErrNotFound
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, path, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg Config
	if _, err := toml.Decode(string(b), &cfg); err != nil {
		return Config{}, path, fmt.Errorf("config: parse %s: %w", path, err)
	}
	// Overlay the gitignored per-user satelle.local.toml beside the committed
	// file; its set fields win. Decoding over the populated cfg leaves absent
	// fields untouched. An absent overlay is not an error.
	localPath := filepath.Join(filepath.Dir(path), LocalConfigName)
	if lb, lerr := os.ReadFile(localPath); lerr == nil {
		if _, derr := toml.Decode(string(lb), &cfg); derr != nil {
			return Config{}, localPath, fmt.Errorf("config: parse %s: %w", localPath, derr)
		}
	} else if !errors.Is(lerr, os.ErrNotExist) {
		return Config{}, path, fmt.Errorf("config: read %s: %w", localPath, lerr)
	}
	return cfg, path, nil
}

// resolvePath finds the committed config: an explicit path, then the
// SATELLE_CONFIG env, then walking up from CWD for .satelle/satelle.toml.
// Returns "" (no error) when none is found.
func resolvePath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if v := strings.TrimSpace(os.Getenv("SATELLE_CONFIG")); v != "" {
		return v, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("config: getwd: %w", err)
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, DefaultDataDir, ConfigName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}
