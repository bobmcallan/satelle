package config

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/docindex"
)

// The process of record is reported, never silently replaced (sty_d6e209aa).
// Two things can make the repository's authored process differ from what governs
// a story, and satelle says so for both:
//
//   - the authored workflows dir is absent or unreadable, so the embedded default
//     route governs (or, when unreadable, nothing may);
//   - a linked worktree carries its OWN copy of the authored substrate and the
//     main tree's copy governs instead, so the worktree's edits are ignored.
//
// This file holds only the structural facts. The wording that names the embedded
// route lives with the route (wfgovern) — config must not import it — and every
// path comes from the resolved process config, never a hard-coded layout.

// Process finding kinds.
const (
	// ProcessAbsent: the workflows dir the process config resolves does not exist.
	ProcessAbsent = "absent"
	// ProcessUnreadable: it exists and cannot be listed, or is not a directory.
	ProcessUnreadable = "unreadable"
	// ProcessChanged, ProcessExtra, ProcessMissing: a file in the worktree's copy
	// of the authored process that differs from, is absent from, or is lacking
	// against the main tree's copy.
	ProcessChanged = "changed"
	ProcessExtra   = "extra"
	ProcessMissing = "missing"
)

// ProcessFinding is one observation about the process of record. Path is the
// resolved workflows dir for absent/unreadable, and a path relative to the
// tree root (slash-separated) for a divergence.
type ProcessFinding struct {
	Kind   string
	Path   string
	Reason string // the underlying error, for unreadable
}

// ProcessFindings classifies the workflows dir the process config resolves, with
// docindex.ProbeDir — the one predicate every loader of that dir uses. It
// returns nothing for a readable dir.
func ProcessFindings(process Config, processRoot string) []ProcessFinding {
	dir := ResolveProcessAuthoredDirs(process, processRoot)["workflows"]
	switch state, err := docindex.ProbeDir(dir); state {
	case docindex.DirAbsent:
		return []ProcessFinding{{Kind: ProcessAbsent, Path: dir}}
	case docindex.DirUnreadable:
		return []ProcessFinding{{Kind: ProcessUnreadable, Path: dir, Reason: err.Error()}}
	}
	return nil
}

// ProcessDivergence compares a linked worktree's own copy of the authored
// substrate dirs and the constitution against the main tree's, and lists every
// changed, extra and missing file by relative path. It returns nil unless
// invokingRoot is a different tree from processRoot and carries its own copy
// (its process data dir exists) — a main tree, a zero-config tree, and a
// worktree with no copy all report nothing.
//
// Every location comes from ResolveProcessAuthoredDirs / ResolveProcessConstitution
// resolved at processRoot, then re-anchored at invokingRoot; a path outside
// processRoot (an absolute substrate root) is shared by both trees and is not
// compared. It is cheap but not free, so callers run it only at the surfaces
// that report it — never at Open and never in the edit-gate hook.
func ProcessDivergence(invokingRoot, processRoot string, process Config) []ProcessFinding {
	if strings.TrimSpace(invokingRoot) == "" || strings.TrimSpace(processRoot) == "" ||
		sameResolvedPath(invokingRoot, processRoot) {
		return nil
	}
	if !wtHasCopy(invokingRoot, processRoot, process) {
		return nil
	}
	var out []ProcessFinding
	dirs := ResolveProcessAuthoredDirs(process, processRoot)
	kinds := make([]string, 0, len(dirs))
	for k := range dirs {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		out = append(out, compareTree(invokingRoot, processRoot, dirs[k])...)
	}
	out = append(out, compareFile(invokingRoot, processRoot, ResolveProcessConstitution(process, processRoot))...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// wtHasCopy reports whether the linked worktree carries its own process data dir.
func wtHasCopy(invokingRoot, processRoot string, process Config) bool {
	rel, ok := plainRelUnder(processRoot, ResolveProcessDataDir(process, processRoot))
	if !ok {
		return false
	}
	info, err := os.Stat(filepath.Join(invokingRoot, rel))
	return err == nil && info.IsDir()
}

// plainRelUnder returns p relative to root when p lies inside root — unlike
// relUnder it does not glob-escape, since the result is joined, not matched.
func plainRelUnder(root, p string) (string, bool) {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// compareTree diffs mainDir's files against the same relative dir under the worktree.
func compareTree(invokingRoot, processRoot, mainDir string) []ProcessFinding {
	rel, ok := plainRelUnder(processRoot, mainDir)
	if !ok {
		return nil
	}
	wtDir := filepath.Join(invokingRoot, rel)
	mainFiles := treeHashes(mainDir)
	wtFiles := treeHashes(wtDir)
	var out []ProcessFinding
	for name, mh := range mainFiles {
		p := filepath.ToSlash(filepath.Join(rel, name))
		switch wh, present := wtFiles[name]; {
		case !present:
			out = append(out, ProcessFinding{Kind: ProcessMissing, Path: p})
		case wh != mh:
			out = append(out, ProcessFinding{Kind: ProcessChanged, Path: p})
		}
	}
	for name := range wtFiles {
		if _, present := mainFiles[name]; !present {
			out = append(out, ProcessFinding{Kind: ProcessExtra, Path: filepath.ToSlash(filepath.Join(rel, name))})
		}
	}
	return out
}

// compareFile diffs one file (the constitution) between the two trees.
func compareFile(invokingRoot, processRoot, mainPath string) []ProcessFinding {
	rel, ok := plainRelUnder(processRoot, mainPath)
	if !ok {
		return nil
	}
	mh, mok := fileHash(mainPath)
	wh, wok := fileHash(filepath.Join(invokingRoot, rel))
	p := filepath.ToSlash(rel)
	switch {
	case mok && !wok:
		return []ProcessFinding{{Kind: ProcessMissing, Path: p}}
	case !mok && wok:
		return []ProcessFinding{{Kind: ProcessExtra, Path: p}}
	case mok && wok && mh != wh:
		return []ProcessFinding{{Kind: ProcessChanged, Path: p}}
	}
	return nil
}

// treeHashes maps every regular file under dir (relative path) to its SHA-256.
// An absent dir is the empty set; an unreadable file is skipped.
func treeHashes(dir string) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return nil
		}
		if h, ok := fileHash(path); ok {
			out[filepath.ToSlash(rel)] = h
		}
		return nil
	})
	return out
}

func fileHash(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum), true
}

// DivergenceNotice renders the report for a worktree whose own copy of the
// authored process differs from the main tree's: every changed, extra and
// missing file by relative path, and which copy governs. Empty when there are
// no findings.
func DivergenceNotice(invokingRoot, processRoot string, findings []ProcessFinding) string {
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "this worktree (%s) carries its own copy of the authored process and it differs from the main tree's (%s) — the main tree's copy governs and the worktree's copy is ignored:",
		invokingRoot, processRoot)
	for _, f := range findings {
		fmt.Fprintf(&b, "\n  %s: %s", f.Kind, f.Path)
	}
	return b.String()
}
