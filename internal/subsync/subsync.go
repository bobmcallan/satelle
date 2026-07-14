// Package subsync materialises a versioned config bundle into a repo
// byte-for-byte (epic:scoped-sync, order:5). It is pure filesystem mechanism —
// no HTTP, no git — so the client transport (internal/hosted) and the CLI verbs
// (satelle sync config deploy/pull) layer on top and stay testable separately.
//
// This is the git-agnostic successor to the removed project-substrate backup
// (25c79c3): scoped sync walks the area dirs directly rather than git-ls-files,
// and Restore writes the bytes a deploy pulls back. The byte-exact writer and
// the safe-path + exclusion guard are the only pieces reused from the old code.
package subsync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// File is one byte-exact restore target: a server-relative path under the data
// dir (forward slashes, no leading ".satelle/", e.g. "skills/my-skill.md",
// "agents.toml", "constitution.md", "tasks/tsk_x.md") and its verbatim bytes.
type File struct {
	Path    string
	Content []byte
}

// Result is Restore's outcome: how many files were written and which excluded
// (local-only) paths were skipped rather than applied (sty_84f14ace).
type Result struct {
	Written int
	// Skipped holds the server-relative paths that excludedLocal refused.
	// Empty when nothing was skipped. Paths are never written to disk.
	Skipped []string
}

// excludedLocal reports paths (relative to the data dir) that are NEVER written
// on a deploy even if a manifest somehow carries them — the local-only /
// generated state a restore must not clobber. This is the structural guard that
// lets a deploy walk only config areas yet still refuse a hostile or corrupt
// manifest that names a live database.
func excludedLocal(rel string) bool {
	rel = filepath.ToSlash(rel)
	switch rel {
	case "satelle.db", "satelle.local.toml", "satelle", "satelle.exe":
		return true
	}
	if strings.HasPrefix(rel, "satelle.db-") { // -wal, -shm
		return true
	}
	// Whole local-only / generated subtrees a config deploy never touches.
	for _, dir := range []string{"logs/", "backups/", "stories/"} {
		if strings.HasPrefix(rel, dir) {
			return true
		}
	}
	return false
}

// Restore writes files under <dataDir>, each byte-for-byte (0o644, parent dirs
// created), overwriting any existing file at the same path. dataDir is the
// repo's resolved .satelle data dir (the workspace-config root the server paths
// are relative to).
//
// Unsafe paths (escape dataDir via cleanRel) still hard-error. Excluded
// (local-only) paths are SKIPPED and listed in Result.Skipped — never written —
// so a hostile or corrupt manifest cannot drop a satelle.db over a live one,
// while a batch that also carries legitimate files (or a partition already
// poisoned with backups/) can still complete and advance a pull cursor
// (sty_84f14ace).
func Restore(dataDir string, files []File) (Result, error) {
	var res Result
	for _, f := range files {
		rel, err := cleanRel(f.Path)
		if err != nil {
			return res, fmt.Errorf("subsync: restore %q: %w", f.Path, err)
		}
		if excludedLocal(rel) {
			res.Skipped = append(res.Skipped, rel)
			continue
		}
		dest := filepath.Join(dataDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return res, fmt.Errorf("subsync: mkdir for %s: %w", rel, err)
		}
		if err := os.WriteFile(dest, f.Content, 0o644); err != nil {
			return res, fmt.Errorf("subsync: write %s: %w", rel, err)
		}
		res.Written++
	}
	return res, nil
}

// cleanRel validates a manifest path and returns its canonical relative form:
// no empty/absolute/backslash paths, no ".", "..", or empty segments, no control
// characters. This is the restore side's guard so a manifest can never escape
// the data dir.
func cleanRel(p string) (string, error) {
	p = strings.TrimSpace(filepath.ToSlash(p))
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, `\`) {
		return "", fmt.Errorf("absolute or backslash path")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("bad segment %q", seg)
		}
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("control character")
		}
	}
	return p, nil
}
