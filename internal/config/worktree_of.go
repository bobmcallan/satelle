package config

import (
	"os"
	"path/filepath"
	"strings"
)

// WorktreeOfMemoised returns the git working-tree root containing dir without
// starting git, from a repo identity MemoGitIdentity already holds
// (sty_a74551d6 AC5). It walks up from dir to the nearest `.git` entry, reads
// which git common directory that entry belongs to from the files git itself
// wrote, and accepts the tree only if that common directory is one the memo
// resolved with `git rev-parse --git-common-dir`. That check is what confirms
// dir is inside a tree the call already asked git about; anything it cannot
// confirm returns ok=false and the caller asks git.
//
// Not confirmed, so ok=false: no memo held or no resolved identity in it, GIT_DIR
// or GIT_WORK_TREE set, no `.git` above dir, or a `.git` whose common directory
// is not a memoised one (a submodule, another repo nested inside the tree). A
// core.worktree setting that moves a working tree away from its `.git` is not
// read; the walk would name the directory holding `.git`.
func WorktreeOfMemoised(dir string) (tree string, ok bool) {
	if os.Getenv("GIT_DIR") != "" || os.Getenv("GIT_WORK_TREE") != "" {
		return "", false
	}
	gitIdentityMemo.mu.Lock()
	known := map[string]bool{}
	for _, r := range gitIdentityMemo.byRoot {
		if r.ok && r.identity != "" {
			known[r.identity] = true
		}
	}
	gitIdentityMemo.mu.Unlock()
	if len(known) == 0 {
		return "", false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	// git prints the symlink-resolved toplevel.
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = resolved
	}
	for cur := filepath.Clean(abs); ; {
		if fi, serr := os.Lstat(filepath.Join(cur, ".git")); serr == nil {
			if common, cok := commonDirOfDotGit(filepath.Join(cur, ".git"), fi); cok && known[common] {
				return cur, true
			}
			return "", false // the nearest `.git` is some other repo's: let git answer
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		cur = parent
	}
}

// commonDirOfDotGit is the git common directory a `.git` entry belongs to, as
// gitCommonIdentity prints it: the symlink-resolved absolute path. A `.git`
// directory is its own common directory. A `.git` file ("gitdir: <path>", a
// linked worktree or submodule) names a git dir, whose `commondir` file, when
// present, names the shared one.
func commonDirOfDotGit(path string, fi os.FileInfo) (string, bool) {
	gitDir := path
	if !fi.IsDir() {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", false
		}
		line := strings.TrimSpace(string(b))
		rest, found := strings.CutPrefix(line, "gitdir:")
		if !found {
			return "", false
		}
		gitDir = strings.TrimSpace(rest)
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(filepath.Dir(path), gitDir)
		}
	}
	common := gitDir
	if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common = strings.TrimSpace(string(b))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
	}
	abs, err := filepath.Abs(common)
	if err != nil {
		return "", false
	}
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = resolved
	}
	return filepath.Clean(abs), true
}
