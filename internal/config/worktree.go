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
	// AbsentWiring is what a dispatch does when a performer would run in a
	// linked worktree whose harness gate wiring is absent (sty_f141c77f): refuse
	// it, or run it visibly ungated. Empty means refuse — an ungated performer
	// is the worse failure, so fail-open is something a repository opts into.
	AbsentWiring string `toml:"absent_wiring"`
}

// The two absent-wiring policies a repository may declare.
const (
	AbsentWiringRefuse   = "refuse"
	AbsentWiringFailOpen = "fail-open"
)

// HookWrapperRel is the parameterised fail-visible hook wrapper every deployed
// harness gate and commit-gate command runs. It is part of satelle's own data
// layout, not provider knowledge, so the wiring guard and the init scaffolding
// share this one spelling.
const HookWrapperRel = ".satelle/hooks/satelle-hook.sh"

// AbsentWiringPolicy resolves [worktree] absent_wiring: AbsentWiringRefuse when
// undeclared, otherwise the declared (validated) value.
func (c Config) AbsentWiringPolicy() string {
	if c.Worktree.AbsentWiring == "" {
		return AbsentWiringRefuse
	}
	return c.Worktree.AbsentWiring
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
		clean, why := relPathProblem(raw)
		if why != "" {
			return bad("include", raw, why)
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
	switch w.AbsentWiring {
	case "", AbsentWiringRefuse, AbsentWiringFailOpen:
	default:
		return fmt.Errorf("config: %s: [worktree] absent_wiring %q — must be %q or %q",
			path, w.AbsentWiring, AbsentWiringRefuse, AbsentWiringFailOpen)
	}
	if w.Branch != "" {
		sample := strings.ReplaceAll(w.Branch, WorktreeIDPlaceholder, "sty_00000000")
		if why := invalidBranchName(sample); why != "" {
			return bad("branch", w.Branch, why)
		}
	}
	return nil
}

// relPathProblem applies the rules every declared repo-relative path shares
// (a [worktree] include entry, a [harness.<name>] gate_wiring entry): it names
// a path inside the repository, not a pattern. It returns the cleaned path, or
// why the entry is refused.
func relPathProblem(raw string) (clean, why string) {
	if strings.TrimSpace(raw) == "" {
		return "", "an empty entry names no path"
	}
	if strings.ContainsAny(raw, "\x00\n\r\t*?[]\\") || strings.HasPrefix(raw, "!") || strings.HasPrefix(raw, "#") {
		return "", "must name a path, not a pattern"
	}
	if filepath.IsAbs(raw) || strings.HasPrefix(raw, "/") {
		return "", "must be relative to the repository root, not absolute"
	}
	clean = filepath.Clean(raw)
	if clean == "." {
		return "", "names the whole repository"
	}
	if !filepath.IsLocal(clean) {
		return "", "points outside the repository"
	}
	if clean == ".git" || strings.HasPrefix(clean, ".git"+string(filepath.Separator)) {
		return "", "names git's own directory"
	}
	return clean, ""
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
