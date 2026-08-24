// runtime.go — home-keyed runtime plane (epic:substrate-planes / sty_4660bbe1).
//
// Authored substrate stays under <repo>/.satelle (ResolveDataDir). Runtime state
// — the per-repo DB, logs, backups, and the stories attachment cache — lives
// under ~/.satelle/<repo-key>/ (ResolveRuntimeDir). One repo-key maps to one
// isolated dir (never a flat multi-repo bag); see decision-local-db-placement
// and decision-substrate-planes-local-first.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// RepoKey returns a stable directory key for repoRoot under GlobalDir().
// Multiple worktrees / cwd forms of the same repository hash identically.
//
// Derivation:
//  1. Prefer git identity via `git rev-parse --git-common-dir` (worktree-collapsing:
//     a linked worktree returns the main repo's .git).
//  2. Fall back to symlink-resolved absolute path for non-git trees.
//
// The key is `<sanitised-basename>-<sha256[:8]>` so `ls ~/.satelle` stays legible
// while collisions stay improbable. Never reads os.Getwd(); the root is an argument.
//
// Note: a tree that gains a .git later (init → git init) can flip identity. Callers
// that need continuity across that flip should use ResolveRuntimeDir, which prefers
// an already-existing runtime dir under either key.
func RepoKey(repoRoot string) string {
	identity, base := repoIdentity(repoRoot)
	return keyFromIdentity(identity, base)
}

// pathRepoKey is the path-only form of RepoKey (ignores git). Used to find a
// runtime dir that was created before the tree became a git repo.
func pathRepoKey(repoRoot string) string {
	identity, base := pathIdentity(repoRoot)
	return keyFromIdentity(identity, base)
}

func keyFromIdentity(identity, base string) string {
	sum := sha256.Sum256([]byte(identity))
	short := hex.EncodeToString(sum[:])[:8]
	return sanitiseBasename(base) + "-" + short
}

// repoIdentity returns a canonical identity string and a human basename for keying.
func repoIdentity(repoRoot string) (identity, basename string) {
	root := strings.TrimSpace(repoRoot)
	if root == "" {
		root = "."
	}
	// Git common-dir: collapses worktrees onto the main repo's .git.
	if id, base, ok := gitCommonIdentity(root); ok {
		return id, base
	}
	return pathIdentity(root)
}

// pathIdentity is the non-git identity: symlink-resolved absolute path.
func pathIdentity(repoRoot string) (identity, basename string) {
	root := strings.TrimSpace(repoRoot)
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = resolved
	}
	abs = filepath.Clean(abs)
	return abs, filepath.Base(abs)
}

// gitCommonIdentity resolves identity via git's common dir. Returns ok=false when
// git is unavailable or the path is not a git work tree.
func gitCommonIdentity(repoRoot string) (identity, basename string, ok bool) {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return "", "", false
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return "", "", false
	}
	// git often returns a relative ".git"; resolve against repoRoot first.
	if !filepath.IsAbs(common) {
		common = filepath.Join(repoRoot, common)
	}
	abs, err := filepath.Abs(common)
	if err != nil {
		return "", "", false
	}
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = resolved
	}
	abs = filepath.Clean(abs)
	// Basename of the parent of .git (the main working tree root).
	base := filepath.Base(filepath.Dir(abs))
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = filepath.Base(abs)
	}
	return abs, base, true
}

// sanitiseBasename keeps only filesystem-safe runes so the key is a single path segment.
func sanitiseBasename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "repo"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "repo"
	}
	// Cap length so a long monorepo name does not make paths absurd.
	if len(out) > 48 {
		out = out[:48]
	}
	return out
}

// RuntimeResolution is the result of ResolveRuntimeDir: the absolute runtime
// directory and whether it is the pre-migration legacy layout under data_dir.
type RuntimeResolution struct {
	Dir    string // absolute path holding satelle.db, logs/, backups/, stories/
	Legacy bool   // true when Dir is still <data_dir> because migration has not run
}

// ResolveRuntimeDir returns where runtime state lives for repoRoot.
//
// Policy (sty_4660bbe1):
//   - An explicit `db` override wins: runtime dir is the parent of that path.
//   - Prefer an existing home-keyed dir: first the current RepoKey (git-aware),
//     then the path-only key (so a tree that was inited non-git and later gained
//     a .git keeps its original ledger).
//   - Else, if a legacy <data_dir>/satelle.db exists, return the legacy dir with
//     Legacy=true so open keeps working and callers can emit a deprecation note
//     (no silent move — migrate is an explicit command).
//   - Else (fresh repo) return the home-keyed path under the current RepoKey.
//
// data_dir overrides do NOT relocate runtime; they only relocate authored substrate.
func (c Config) ResolveRuntimeDir(repoRoot string) RuntimeResolution {
	if p := strings.TrimSpace(c.DB); p != "" {
		db := resolveUnder(repoRoot, p)
		return RuntimeResolution{Dir: filepath.Dir(db), Legacy: false}
	}

	primary := filepath.Join(GlobalDir(), RepoKey(repoRoot))
	if fileExists(filepath.Join(primary, DefaultDBName)) {
		return RuntimeResolution{Dir: primary, Legacy: false}
	}
	// Continuity across "init then git init": path-key DB created before .git.
	pathKey := filepath.Join(GlobalDir(), pathRepoKey(repoRoot))
	if pathKey != primary && fileExists(filepath.Join(pathKey, DefaultDBName)) {
		return RuntimeResolution{Dir: pathKey, Legacy: false}
	}

	legacyDir := c.ResolveDataDir(repoRoot)
	legacyDB := filepath.Join(legacyDir, DefaultDBName)
	if fileExists(legacyDB) {
		return RuntimeResolution{Dir: legacyDir, Legacy: true}
	}
	return RuntimeResolution{Dir: primary, Legacy: false}
}

// ResolveRuntimeDB returns the absolute path of the per-repo sqlite database under
// the resolved runtime dir. An explicit `db` config still wins via ResolveRuntimeDir.
func (c Config) ResolveRuntimeDB(repoRoot string) string {
	return filepath.Join(c.ResolveRuntimeDir(repoRoot).Dir, DefaultDBName)
}

// ResolveLogsDir is the sole storage path for this repo's runtime logs
// (~/.satelle/<repo-key>/logs). The in-repo .satelle/logs pointer is not
// storage; it is a symlink to this directory.
func (c Config) ResolveLogsDir(repoRoot string) string {
	return filepath.Join(c.ResolveRuntimeDir(repoRoot).Dir, "logs")
}

// LegacyRuntimeNote is the one-line deprecation message when a repo still has
// its database under the authored data dir. Empty when not legacy.
func (c Config) LegacyRuntimeNote(repoRoot string) string {
	res := c.ResolveRuntimeDir(repoRoot)
	if !res.Legacy {
		return ""
	}
	target := filepath.Join(GlobalDir(), RepoKey(repoRoot))
	return fmt.Sprintf(
		"satelle: runtime state still under %s — run `satelle runtime migrate` to move it to %s (see decision-substrate-planes-local-first)",
		res.Dir, target)
}

// fileExists reports a regular file (or any non-dir node) at path.
func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// RuntimeKeyDirPattern matches a home-keyed runtime directory name
// (`<sanitised-basename>-<sha256[:8]>`). Shared by CLI list and the
// integration host-surface guard (sty_c36c211f).
var RuntimeKeyDirPattern = regexp.MustCompile(`^[^/]+-[0-9a-f]{8}$`)

// IsRuntimeKeyDir reports whether name is a home-keyed runtime key segment.
func IsRuntimeKeyDir(name string) bool {
	return RuntimeKeyDirPattern.MatchString(name)
}

// RepoPathMarkerName is the file written into a runtime dir recording the
// absolute repo root that owns it (sty_c36c211f AC2 — forward resolution).
const RepoPathMarkerName = "repo.path"

// WriteRepoPathMarker records repoRoot into runtimeDir/repo.path so
// `satelle runtime list` can reverse-map keys. Best-effort: no-op when
// runtimeDir is empty or equals the repo's data dir (legacy layout).
func WriteRepoPathMarker(runtimeDir, repoRoot string) error {
	runtimeDir = strings.TrimSpace(runtimeDir)
	repoRoot = strings.TrimSpace(repoRoot)
	if runtimeDir == "" || repoRoot == "" {
		return nil
	}
	// Only mark home-keyed dirs (parent is GlobalDir, basename is a key).
	base := filepath.Base(runtimeDir)
	if !IsRuntimeKeyDir(base) {
		return nil
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return err
	}
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		abs = repoRoot
	}
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = resolved
	}
	return os.WriteFile(filepath.Join(runtimeDir, RepoPathMarkerName), []byte(abs+"\n"), 0o644)
}

// ReadRepoPathMarker returns the recorded repo root for a runtime dir, or "" if absent.
func ReadRepoPathMarker(runtimeDir string) string {
	b, err := os.ReadFile(filepath.Join(runtimeDir, RepoPathMarkerName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
