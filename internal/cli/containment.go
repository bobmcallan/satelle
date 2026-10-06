package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bobmcallan/satelle/internal/worktree"
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
//	  && !under(containmentTempRoots)       // temp / scratch → allow, even a git root
//	  && gitRootOf(absTarget) != ""         // no enclosing tree → allow
//	  && gitRootOf(absTarget) != clean(anchor)
//	  && !sameRepo(anchor, gitRootOf(...))  // linked worktree of home → allow
//
// Non-repo paths ($HOME without a .git, /dev/null) and anything under the temp
// dir are outside the fence's concern, as is a linked worktree of the session's
// own repository (same git common dir; the ordinary edit gate still judges it —
// sty_bcf837ff). The fence denies only ANOTHER repo's tree, and an unresolvable
// common dir counts as another repo (fail closed).

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

// containmentTempRoots is the one set of temp roots both the foreign-tree fence
// and the temp-draft edit exemption (tempDraftTarget) honour. A var so a test
// whose repos live under t.TempDir can point it elsewhere and keep proving the
// fence and the edit gate for trees outside the temp roots.
var containmentTempRoots = tempDraftRoots

// commonDirTimeout bounds each git lookup so a wedged repo cannot hang a hook.
const commonDirTimeout = 2 * time.Second

var (
	commonDirMu    sync.Mutex
	commonDirCache = map[string]string{}
)

// commonDirOf is the git common dir of the tree at root, memoised for the
// process (a Bash event checks the anchor once per candidate). Only successes
// are cached, so a repo that becomes readable later is not stuck as foreign.
func commonDirOf(root string) (string, bool) {
	commonDirMu.Lock()
	dir, ok := commonDirCache[root]
	commonDirMu.Unlock()
	if ok {
		return dir, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), commonDirTimeout)
	defer cancel()
	dir, err := worktree.CommonDir(ctx, root)
	if err != nil || dir == "" {
		return "", false
	}
	commonDirMu.Lock()
	commonDirCache[root] = dir
	commonDirMu.Unlock()
	return dir, true
}

// sameRepo reports whether the trees at anchor and root share one git common
// dir — a main tree and its linked worktrees. Any lookup failure is false, so
// the fence fails closed.
func sameRepo(anchor, root string) bool {
	a, ok := commonDirOf(anchor)
	if !ok {
		return false
	}
	r, ok := commonDirOf(root)
	return ok && a == r
}

// foreignRootFor is the one predicate behind both the Edit and Bash fences: the
// git root abs lives in when that is ANOTHER repository's tree, ok=false when
// abs is allowed (in the anchor, temp, non-repo, or a linked worktree of the
// anchor's repository). abs must be absolute and clean.
func foreignRootFor(anchor, abs string) (root string, ok bool) {
	if withinRoot(anchor, abs) {
		return "", false
	}
	for _, t := range containmentTempRoots() {
		if withinRoot(t, abs) {
			return "", false
		}
	}
	root = gitRootOf(abs)
	if root == "" || root == filepath.Clean(anchor) || sameRepo(anchor, root) {
		return "", false
	}
	return root, true
}

// linkedTreeTarget reports whether abs lives in a tree the fence lets through
// only because it shares the anchor's git common dir — a linked worktree of the
// session repository, outside the anchor and outside the temp roots. Such a
// target is still a tree change: the ordinary edit gate must judge it
// (sty_bcf837ff). abs must be absolute and clean.
func linkedTreeTarget(anchor, abs string) bool {
	if withinRoot(anchor, abs) {
		return false
	}
	for _, t := range containmentTempRoots() {
		if withinRoot(t, abs) {
			return false
		}
	}
	root := gitRootOf(abs)
	return root != "" && root != filepath.Clean(anchor) && sameRepo(anchor, root)
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
		if root, foreign := foreignRootFor(anchor, c); foreign {
			return c, root, true
		}
	}
	return "", "", false
}

// treeOf is the git working tree an edit target lives in, "" when it is in none
// (temp, scratch) or there is no target (a Bash event). It is how a session that
// holds seats in several worktrees attributes an edit to the seat of the tree
// the edit lands in (sty_42231b74); gitRootOf already walks up from the nearest
// existing directory, so a file not yet created still resolves.
func treeOf(target string) string {
	root := sessionAnchor()
	if strings.TrimSpace(target) == "" || strings.TrimSpace(root) == "" {
		return ""
	}
	return gitRootOf(resolveAbsTarget(root, target))
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
	return foreignRootFor(filepath.Clean(root), resolveAbsTarget(root, target))
}
