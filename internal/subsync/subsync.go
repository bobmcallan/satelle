// Package subsync bundles a repo's authored .satelle/ substrate for a hosted
// backup push and restores it byte-for-byte on pull (sty_f2c1899a). It is pure
// filesystem mechanism — no HTTP — so the client transport (internal/hosted) and
// the CLI verbs (satelle project push/pull) layer on top and stay testable
// separately.
//
// The push set is exactly the repo's git-TRACKED files under .satelle/ (authored
// substrate is committed; local/generated state is gitignored), so the whole
// AC5 guardrail — never upload satelle.db + WAL/SHM, logs/, backups/, the
// generated story/index views, satelle.local.toml, or the pinned binary — falls
// out of using git as the source of truth. A second hard exclusion filter runs
// over the tracked list anyway, so a repo that has mistakenly tracked one of
// those local-only files still never uploads it.
package subsync

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// substrateDir is the authored substrate root within a repo.
const substrateDir = ".satelle"

// Bundle collects the repo's git-tracked authored substrate under .satelle/ and
// reads each file's verbatim bytes. Paths are returned relative to .satelle/
// (forward slashes), sorted, with the top-level dir as Kind. Only git-tracked,
// non-excluded files are included (AC5). It errors when repoRoot is not a git
// work tree (the tracked set is undefined without git).
func Bundle(repoRoot string) ([]hosted.SubstrateFile, error) {
	tracked, err := gitTrackedSubstrate(repoRoot)
	if err != nil {
		return nil, err
	}
	files := make([]hosted.SubstrateFile, 0, len(tracked))
	for _, repoRel := range tracked {
		rel := toRel(repoRel)
		if rel == "" || excluded(rel) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(repoRel)))
		if err != nil {
			return nil, fmt.Errorf("subsync: read %s: %w", repoRel, err)
		}
		files = append(files, hosted.SubstrateFile{Path: rel, Kind: kindOf(rel), Content: content})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// Restore writes files into <repoRoot>/.satelle/, each byte-for-byte (0o644,
// parent dirs created), overwriting any existing file at the same path. It
// refuses any path that is unsafe (escapes .satelle/) or excluded (so a hostile
// or corrupt bundle can never drop a satelle.db over a live one). It returns the
// number of files written.
func Restore(repoRoot string, files []hosted.SubstrateFile) (int, error) {
	root := filepath.Join(repoRoot, substrateDir)
	written := 0
	for _, f := range files {
		rel, err := cleanRel(f.Path)
		if err != nil {
			return written, fmt.Errorf("subsync: restore %q: %w", f.Path, err)
		}
		if excluded(rel) {
			return written, fmt.Errorf("subsync: restore %q: refusing to write a local-only path", f.Path)
		}
		dest := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return written, fmt.Errorf("subsync: mkdir for %s: %w", rel, err)
		}
		if err := os.WriteFile(dest, f.Content, 0o644); err != nil {
			return written, fmt.Errorf("subsync: write %s: %w", rel, err)
		}
		written++
	}
	return written, nil
}

// gitTrackedSubstrate returns the repo-relative (forward-slash) paths of every
// git-tracked file under .satelle/. -z keeps paths literal (no quoting), so a
// path with odd characters round-trips exactly.
func gitTrackedSubstrate(repoRoot string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoRoot, "ls-files", "-z", "--", substrateDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("subsync: git ls-files (is %s a git work tree?): %s", repoRoot, msg)
	}
	var out []string
	for _, p := range strings.Split(stdout.String(), "\x00") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, filepath.ToSlash(p))
		}
	}
	return out, nil
}

// toRel converts a repo-relative substrate path (".satelle/skills/x.md") to a
// path relative to .satelle/ ("skills/x.md"). A path not under .satelle/ (which
// git ls-files -- .satelle should never return) collapses to "".
func toRel(repoRel string) string {
	rel := strings.TrimPrefix(filepath.ToSlash(repoRel), substrateDir+"/")
	if rel == repoRel { // no prefix stripped → not under .satelle/
		return ""
	}
	return rel
}

// kindOf promotes the top-level substrate dir as a file's Kind ("skills/x.md" →
// "skills"); a root file (constitution.md, agents.toml) has no kind.
func kindOf(rel string) string {
	if i := strings.IndexByte(rel, '/'); i > 0 {
		return rel[:i]
	}
	return ""
}

// excluded reports paths (relative to .satelle/) that are never backed up even
// if a repo has mistakenly git-tracked them — the local-only / generated state
// the AC5 guardrail enumerates. This is the belt over git ls-files's braces
// (the generated index.md/log.md views are gitignored, so git already drops them).
func excluded(rel string) bool {
	rel = filepath.ToSlash(rel)
	// Local databases + their sidecars, the per-user config overlay, and the
	// pinned local binary.
	switch rel {
	case "satelle.db", "satelle.local.toml", "satelle", "satelle.exe":
		return true
	}
	if strings.HasPrefix(rel, "satelle.db-") { // -wal, -shm
		return true
	}
	// Whole local-only / generated subtrees.
	for _, dir := range []string{"logs/", "backups/", "stories/", "documents/story-implementation-summary/"} {
		if strings.HasPrefix(rel, dir) {
			return true
		}
	}
	return false
}

// cleanRel validates a bundle path and returns its canonical relative form,
// mirroring the server's substrate.CleanPath: no empty/absolute/backslash paths,
// no ".", "..", or empty segments, no control characters. This is the restore
// side's guard so a bundle can never escape .satelle/.
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
