package cli

import (
	"os"
	"path/filepath"
	"strings"
)

// containment.go — filesystem layer for the foreign-tree fence (sty_a8454d10 /
// epic:substrate-planes). bashscan.go stays a pure candidate extractor; this
// package walks the real FS to decide whether a candidate lives in another
// git working tree.
//
// Predicate (shared by Bash + Edit gates):
//
//	foreign(anchor, absTarget) :=
//	  !withinRoot(anchor, absTarget)        // inside home → allow, no stat
//	  && gitRootOf(absTarget) != ""         // no enclosing tree → allow
//	  && gitRootOf(absTarget) != clean(anchor)
//
// Non-repo paths (/tmp, scratchpads, $HOME without a .git, /dev/null) are
// outside the fence's concern. The fence denies only ANOTHER repo's tree.

// gitRootOf walks ancestors of abs for a .git entry (directory or file —
// worktree/submodule form). Start at filepath.Dir(abs) so a not-yet-created
// target file still finds its parent tree. Nearest wins; stop at filesystem
// root; no symlink resolution (matches bashscan's honest scope). Returns ""
// when no enclosing git tree is found.
func gitRootOf(abs string) string {
	abs = filepath.Clean(abs)
	if abs == "" || abs == "." {
		return ""
	}
	// Prefer the directory containing the path; if abs is itself a dir that
	// holds .git, check it too after walking parents of a file path.
	dir := abs
	if fi, err := os.Lstat(abs); err == nil && !fi.IsDir() {
		dir = filepath.Dir(abs)
	} else if err != nil {
		// Path does not exist yet — walk from its parent.
		dir = filepath.Dir(abs)
	}
	for {
		gitEntry := filepath.Join(dir, ".git")
		if fi, err := os.Lstat(gitEntry); err == nil {
			// Directory or file (worktree/submodule) both mark a root.
			if fi.IsDir() || fi.Mode().IsRegular() {
				return filepath.Clean(dir)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// foreignTreeTarget filters candidate absolute paths and returns the first
// that lands in a git working tree whose root differs from anchor, plus that
// foreign root. Empty path / ok=false means nothing foreign (allow).
func foreignTreeTarget(anchor string, candidates []string) (path string, foreignRoot string, ok bool) {
	anchor = filepath.Clean(anchor)
	if anchor == "" || anchor == "." {
		return "", "", false
	}
	for _, c := range candidates {
		c = filepath.Clean(c)
		if c == "" {
			continue
		}
		if withinRoot(anchor, c) {
			continue
		}
		root := gitRootOf(c)
		if root == "" {
			continue // non-repo path — not fenced
		}
		if root == anchor {
			continue // same tree, other path spelling
		}
		return c, root, true
	}
	return "", "", false
}

// editTargetForeign reports whether an Edit/Write target resolves into a
// foreign git working tree relative to the session anchor. false when the
// path is in-home, non-repo, or the anchor is unresolvable (stay conservative
// — engaged-story rules still apply rather than free-passing).
func editTargetForeign(target string) (foreignRoot string, foreign bool) {
	root := sessionAnchor()
	if strings.TrimSpace(root) == "" {
		return "", false
	}
	abs := resolveAbsTarget(root, target)
	if withinRoot(root, abs) {
		return "", false
	}
	gitRoot := gitRootOf(abs)
	if gitRoot == "" || gitRoot == filepath.Clean(root) {
		return "", false
	}
	return gitRoot, true
}
