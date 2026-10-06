package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// WorktreeIDPlaceholder is the one placeholder a [worktree] template accepts.
const WorktreeIDPlaceholder = "{id}"

// WorktreeConfig is the [worktree] table (sty_804c566b): what a worktree opened
// for a story needs. It is operator-declared and the binary carries no default
// for any key — which paths a repo needs, and how it names its branches and
// places its trees, is that repo's configuration, never a Go constant.
type WorktreeConfig struct {
	// Include lists repo-relative paths the main tree gitignores and a worktree
	// needs (harness wiring, local tool config, env files). Each is carried into
	// the worktree as a link to the main tree's path, so nothing drifts and no
	// secret is copied. A path names a file or directory, never a harness.
	Include []string `toml:"include"`
	// Branch and Path are templates for the branch and the location of a new
	// worktree. {id} is the only placeholder. A relative Path resolves against
	// the main tree's root. Unset means the matching flag is required.
	Branch string `toml:"branch"`
	Path   string `toml:"path"`
}

// validateWorktree refuses a malformed [worktree] at load time, naming the
// entry. It only reads: a refusal never rewrites the configuration or any
// substrate.
func validateWorktree(cfg Config, path string) error {
	w := cfg.Worktree
	root := RepoRootFromConfigPath(path)
	bad := func(key, entry, why string) error {
		return fmt.Errorf("config: %s: [worktree] %s entry %q — %s", path, key, entry, why)
	}
	// The data dir is never carried (sty_ddbe2669 resolves the process from the
	// main tree): refuse the configured one and the conventional one, so a
	// repo that later relocates its data dir cannot start carrying the old one.
	dataDirs := []string{filepath.Join(root, DefaultDataDir), cfg.ResolveDataDir(root)}
	seen := map[string]string{}
	for _, raw := range w.Include {
		if strings.TrimSpace(raw) == "" {
			return bad("include", raw, "an empty entry names no path")
		}
		if strings.ContainsAny(raw, "\x00\n\r\t*?[]\\") || strings.HasPrefix(raw, "!") || strings.HasPrefix(raw, "#") {
			return bad("include", raw, "must name a path, not a pattern")
		}
		if filepath.IsAbs(raw) || strings.HasPrefix(raw, "/") {
			return bad("include", raw, "must be relative to the repository root, not absolute")
		}
		clean := filepath.Clean(raw)
		if clean == "." {
			return bad("include", raw, "names the whole repository")
		}
		if !filepath.IsLocal(clean) {
			return bad("include", raw, "points outside the repository")
		}
		if clean == ".git" || strings.HasPrefix(clean, ".git"+string(filepath.Separator)) {
			return bad("include", raw, "names git's own directory")
		}
		abs := filepath.Join(root, clean)
		for _, dd := range dataDirs {
			if within(dd, abs) || within(abs, dd) {
				return bad("include", raw, "is or contains the data dir, which a worktree never carries")
			}
		}
		if prev, dup := seen[clean]; dup {
			return bad("include", raw, fmt.Sprintf("duplicates %q", prev))
		}
		seen[clean] = raw
	}
	for _, t := range []struct{ key, val string }{{"branch", w.Branch}, {"path", w.Path}} {
		if t.val == "" {
			continue
		}
		if strings.TrimSpace(t.val) == "" {
			return bad(t.key, t.val, "template is blank")
		}
		if !strings.Contains(t.val, WorktreeIDPlaceholder) {
			return bad(t.key, t.val, "template has no "+WorktreeIDPlaceholder+", so every story would collide")
		}
	}
	if w.Branch != "" {
		sample := strings.ReplaceAll(w.Branch, WorktreeIDPlaceholder, "sty_00000000")
		if why := invalidBranchName(sample); why != "" {
			return bad("branch", w.Branch, why)
		}
	}
	return nil
}

// within reports whether p is dir itself or lies under it.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

// invalidBranchName applies git's ref-name rules (git check-ref-format) to a
// branch name and returns why it is refused, or "" when it is acceptable.
func invalidBranchName(name string) string {
	switch {
	case name == "" || name == "@":
		return "is not a valid branch name"
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/"):
		return "must not begin or end with /"
	case strings.HasSuffix(name, "."):
		return "must not end with ."
	case strings.HasPrefix(name, "-"):
		return "must not begin with -"
	case strings.Contains(name, "//"):
		return "must not contain //"
	case strings.Contains(name, ".."):
		return "must not contain .."
	case strings.Contains(name, "@{"):
		return "must not contain @{"
	}
	if strings.ContainsAny(name, " ~^:?*[\\\x7f") {
		return "contains a character git refuses in a branch name"
	}
	for _, r := range name {
		if r < 0x20 {
			return "contains a control character"
		}
	}
	for _, comp := range strings.Split(name, "/") {
		if strings.HasPrefix(comp, ".") || strings.HasSuffix(comp, ".lock") {
			return "has a component beginning with . or ending in .lock"
		}
	}
	return ""
}
