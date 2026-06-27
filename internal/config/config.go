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
	// DefaultWebPort is the local web server's listen port.
	DefaultWebPort = 8787
	// DefaultLogLevel is arbor's level when log_level is unset.
	DefaultLogLevel = "info"
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
	// Review opts this repo into reviewer-gated work. Off by default — the
	// rubrics ship embedded, but ENFORCEMENT is the operator's choice (the
	// process is configured, not hardcoded-on).
	Review ReviewConfig `toml:"review"`
}

// ReviewConfig toggles the quality-management gates for a repo.
type ReviewConfig struct {
	// GateCreate runs the required-structure reviewer on story/task creation,
	// pushing non-conforming drafts back instead of persisting them.
	GateCreate bool `toml:"gate_create"`
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
