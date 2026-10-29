package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// ScratchRepoKey is a short, stable, filesystem-safe stand-in for the repo
// root, so scratch dirs for different repos never collide under the shared temp
// root. (Distinct from RepoKey, which keys the durable runtime dir.)
func ScratchRepoKey(repoRoot string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(repoRoot)))
	return hex.EncodeToString(sum[:])[:12]
}

// scratchBase is the temp root the scratch tree hangs off. Inside a dispatch,
// TMPDIR is the per-dispatch scratch directory itself, so os.TempDir() there
// would nest the story scratch parent INSIDE the dispatch dir — and finishing
// the dispatch would take it (and any tidied files) along. When SATELLE_SCRATCH
// names a dispatch dir of the standard layout, <base>/satelle/<key>/<story>/
// <dispatch>, the base is recovered from it instead.
func scratchBase() string {
	if env := filepath.Clean(os.Getenv(ScratchEnv)); env != "." && env != "" {
		satelleDir := filepath.Dir(filepath.Dir(filepath.Dir(env)))
		if filepath.Base(satelleDir) == "satelle" {
			return filepath.Dir(satelleDir)
		}
	}
	return os.TempDir()
}

// ScratchRoot is the shared scratch tree, <tmp>/satelle, every story's scratch
// hangs off. It is disposable by construction: dispatches and `runtime reap`
// remove what lies under it.
func ScratchRoot() string {
	return filepath.Join(scratchBase(), "satelle")
}

// IsScratchPath reports whether p is ScratchRoot or lies under it. Both sides
// resolve symlinks where they exist (a /tmp that is a link must still match),
// falling back to the cleaned path.
func IsScratchPath(p string) bool {
	root := resolvedPath(ScratchRoot())
	path := resolvedPath(p)
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

func resolvedPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// StoryScratchDir is the story-level scratch parent, <tmp>/satelle/<repo-key>/
// <story>/. Every per-dispatch scratch directory is a child of it, and the tidy
// area (TidyDir) is a sibling of those children — so it outlives any one
// dispatch. One definition, shared by the dispatch layout and `story tidy`, so
// the two cannot drift (sty_d74e9b1b).
func StoryScratchDir(repoRoot, storyID string) string {
	return StoryScratchDirIn(scratchBase(), repoRoot, storyID)
}

// StoryScratchDirIn is StoryScratchDir under an explicit temp root. The
// dispatcher, which runs outside any dispatch, names os.TempDir() itself.
func StoryScratchDirIn(base, repoRoot, storyID string) string {
	return filepath.Join(base, "satelle", ScratchRepoKey(repoRoot), storyID)
}

// TidyDir is where `satelle story tidy` parks files the story itself created.
// It is never a per-dispatch directory, so finishing a dispatch cannot remove it.
func TidyDir(repoRoot, storyID string) string {
	return filepath.Join(StoryScratchDir(repoRoot, storyID), "tidy")
}
