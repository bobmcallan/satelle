package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// GrokConfigName is Grok's user-level config filename under ~/.grok.
// [compat.*] lives only here (not project-scoped) — see Grok docs on harness
// compatibility.
const GrokConfigName = "config.toml"

// GrokTrustedFoldersName is Grok's folder-trust store under ~/.grok. Project
// hooks under <repo>/.grok/hooks/ load only when the folder is trusted here
// (same file /hooks-trust writes).
const GrokTrustedFoldersName = "trusted_folders.toml"

// GrokConfigPath returns ~/.grok/config.toml. os.UserHomeDir honors HOME, so
// tests can t.Setenv("HOME", tmp) without a satelle-specific override.
func GrokConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: resolve home for Grok config: %w", err)
	}
	return filepath.Join(home, ".grok", GrokConfigName), nil
}

// GrokTrustedFoldersPath returns ~/.grok/trusted_folders.toml (HOME-honoring).
func GrokTrustedFoldersPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: resolve home for Grok trust store: %w", err)
	}
	return filepath.Join(home, ".grok", GrokTrustedFoldersName), nil
}

// grokFolderSection is the TOML table name for one trusted path, e.g.
// folders."/home/you/project" (quoted key required for absolute paths).
func grokFolderSection(abs string) string {
	// Escape " and \ for a TOML basic string key.
	esc := strings.ReplaceAll(abs, `\`, `\\`)
	esc = strings.ReplaceAll(esc, `"`, `\"`)
	return `folders."` + esc + `"`
}

// GrokFolderTrusted reports whether content already marks abs as trusted=true.
func GrokFolderTrusted(content, abs string) bool {
	if abs == "" {
		return false
	}
	lines := strings.Split(content, "\n")
	start, end := sectionRange(lines, grokFolderSection(abs))
	if start < 0 {
		return false
	}
	for i := start; i < end; i++ {
		if !isKeyLine(lines[i], "trusted") {
			continue
		}
		// accepted: trusted = true (optional spaces)
		rest := strings.TrimSpace(lines[i])
		eq := strings.Index(rest, "=")
		if eq < 0 {
			continue
		}
		val := strings.TrimSpace(rest[eq+1:])
		val = strings.Trim(val, `"'`)
		return strings.EqualFold(val, "true")
	}
	return false
}

// EnsureGrokFolderTrusted records abs(repoRoot) as trusted in
// ~/.grok/trusted_folders.toml so Grok loads project hooks for that folder.
// Only that path is written; other folder entries are preserved. Idempotent:
// already-trusted leaves the file bytes unchanged (changed=false).
//
// Returns changed, absolute path written, error.
func EnsureGrokFolderTrusted(repoRoot string) (changed bool, abs string, err error) {
	abs, err = filepath.Abs(repoRoot)
	if err != nil {
		return false, "", fmt.Errorf("config: resolve repo root for Grok trust: %w", err)
	}
	abs = filepath.Clean(abs)
	path, err := GrokTrustedFoldersPath()
	if err != nil {
		return false, abs, err
	}
	var before string
	raw, rerr := os.ReadFile(path)
	switch {
	case rerr == nil:
		before = string(raw)
	case os.IsNotExist(rerr):
		// empty
	default:
		return false, abs, fmt.Errorf("config: read %s: %w", path, rerr)
	}
	if GrokFolderTrusted(before, abs) {
		return false, abs, nil
	}
	sec := grokFolderSection(abs)
	after := UpsertKey(before, sec, "trusted", "true")
	after = UpsertKey(after, sec, "decided_at", strconv.FormatInt(time.Now().Unix(), 10))
	if after == before {
		return false, abs, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, abs, fmt.Errorf("config: mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		return false, abs, fmt.Errorf("config: write %s: %w", path, err)
	}
	return true, abs, nil
}
