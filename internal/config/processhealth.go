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
// returns nothing for a readable dir. With a project tier that owns workflows,
// the dir classified is the tier's: the repo's copy does not govern.
func ProcessFindings(process Config, processRoot string) []ProcessFinding {
	dir := ResolveAuthoredRoots(process, processRoot, "").WorkflowsDir()
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
// Every location comes from ResolveAuthoredRoots / ResolveProcessConstitution
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
	return DiffTreeHashes(hashesAt(process, processRoot, processRoot), hashesAt(process, processRoot, invokingRoot))
}

// ProcessHashes maps every file of the authored process at processRoot — each
// ResolveAuthoredRoots repo dir and the ResolveProcessConstitution file — to its
// SHA-256, keyed by a slash-separated path relative to processRoot. A location
// outside processRoot (an absolute substrate root) is left out. It is the one
// walk of the process: ProcessDivergence compares two of these maps, and a remote
// node reports one so local satelle can see what changed on it.
func ProcessHashes(process Config, processRoot string) map[string]string {
	return hashesAt(process, processRoot, processRoot)
}

// hashesAt is ProcessHashes with each location, resolved at processRoot,
// re-anchored at anchor before it is read; the keys stay relative to processRoot.
func hashesAt(process Config, processRoot, anchor string) map[string]string {
	out := map[string]string{}
	// The trees are compared: a project tier is not part of either tree.
	for _, dir := range ResolveAuthoredRoots(process, processRoot, "").Dirs {
		rel, ok := plainRelUnder(processRoot, dir)
		if !ok {
			continue
		}
		for name, h := range treeHashes(filepath.Join(anchor, rel)) {
			out[filepath.ToSlash(filepath.Join(rel, name))] = h
		}
	}
	if rel, ok := plainRelUnder(processRoot, ResolveProcessConstitution(process, processRoot)); ok {
		if h, ok := fileHash(filepath.Join(anchor, rel)); ok {
			out[filepath.ToSlash(rel)] = h
		}
	}
	return out
}

// DiffTreeHashes lists, sorted by path, every file in before that after lacks
// (ProcessMissing) or differs on (ProcessChanged), and every file only after has
// (ProcessExtra).
func DiffTreeHashes(before, after map[string]string) []ProcessFinding {
	var out []ProcessFinding
	for p, bh := range before {
		switch ah, present := after[p]; {
		case !present:
			out = append(out, ProcessFinding{Kind: ProcessMissing, Path: p})
		case ah != bh:
			out = append(out, ProcessFinding{Kind: ProcessChanged, Path: p})
		}
	}
	for p := range after {
		if _, present := before[p]; !present {
			out = append(out, ProcessFinding{Kind: ProcessExtra, Path: p})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
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
