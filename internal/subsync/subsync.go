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

	"github.com/bobmcallan/satelle/internal/config"
)

// File is one byte-exact restore target: a server-relative path under the data
// dir (forward slashes, no leading ".satelle/", e.g. "skills/my-skill.md",
// "agents.toml", "constitution.md", "tasks/tsk_x.md") and its verbatim bytes.
type File struct {
	Path    string
	Content []byte
}

// Result is Restore's outcome: how many files were written, which excluded
// (local-only) paths were skipped rather than applied (sty_84f14ace), and which
// files could not be written at all (sty_4c3729e7).
type Result struct {
	Written int
	// Skipped holds the server-relative paths that excludedLocal refused.
	// Empty when nothing was skipped. Paths are never written to disk.
	Skipped []string
	// Failed holds files Restore WANTED to write and could not — a filesystem
	// condition, not policy. Deliberately distinct from Skipped: an operator
	// reading "skipped" learns something routine happened, and a real failure
	// must never be reported that way. A non-empty Failed is not an error by
	// itself; each caller decides (a cursor-driven pull continues, a deliberate
	// single-file or whole-partition write fails).
	Failed []FileError
}

// FileError is one file Restore could not write, with the reason.
type FileError struct {
	Path string
	Err  error
}

func (e FileError) Error() string { return e.Path + ": " + e.Err.Error() }

// Err joins the failures into one error, or nil when there are none. Callers for
// which a partial restore is a failure (config deploy, single-file publish) use
// this so their loud behaviour is preserved.
func (r Result) Err() error {
	if len(r.Failed) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(r.Failed))
	for _, f := range r.Failed {
		msgs = append(msgs, f.Error())
	}
	return fmt.Errorf("subsync: %d file(s) not written: %s", len(r.Failed), strings.Join(msgs, "; "))
}

// excludedLocal reports paths (relative to the data dir) that are NEVER written
// on a deploy even if a manifest somehow carries them — the local-only /
// generated state a restore must not clobber. This is the structural guard that
// lets a deploy walk only config areas yet still refuse a hostile or corrupt
// manifest that names a live database. The .local segment rule is shared with
// the push bundler (config.LocalOnlyPath) so a file that reached the server from
// an older client cannot be written back down either.
func excludedLocal(rel string) bool {
	rel = filepath.ToSlash(rel)
	if config.LocalOnlyPath(rel) {
		return true
	}
	switch rel {
	case "satelle.db", "satelle", "satelle.exe":
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

// ExcludedLocal reports whether a server-relative path is one Restore will
// never write, so a caller can skip it before spending a fetch (sty_0fd04503).
// Restore remains the enforcement point; this is only an early-out.
//
// Returns false when the path fails cleanRel (absolute, "..", empty, …): those
// are Restore hard-errors, not skips — pre-filtering them would swallow the
// escape guard.
func ExcludedLocal(p string) bool {
	rel, err := cleanRel(p)
	if err != nil {
		return false
	}
	return excludedLocal(rel)
}

// Restore writes files under <dataDir>, each byte-for-byte, parent dirs created,
// overwriting any existing file at the same path. dataDir is the repo's resolved
// .satelle data dir (the workspace-config root the server paths are relative to).
//
// THREE outcomes per file, and they are not interchangeable:
//
//   - Unsafe paths (escape dataDir via cleanRel) still HARD-ERROR. That is the
//     escape guard; a manifest that names one is hostile or corrupt.
//   - Excluded (local-only) paths are SKIPPED and listed in Result.Skipped —
//     never written — so a corrupt manifest cannot drop a satelle.db over a live
//     one, while a batch carrying legitimate files can still complete and
//     advance a pull cursor (sty_84f14ace).
//   - A file that cannot be WRITTEN is recorded in Result.Failed and the batch
//     CONTINUES (sty_4c3729e7). Returning on the first write failure wedged the
//     documents pull permanently: the cursor save sits after that return, so
//     every later pull re-fetched the same batch and failed on the same file.
//
// Mode: an existing destination keeps its current permissions, a new one is
// created 0o644, and a file whose content carries the `generated: satelle`
// frontmatter marker is forced to 0o444 — the mode the OKF materializer writes
// its views with. See writeRestored for why a read-only destination is replaced
// rather than refused.
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
			res.Failed = append(res.Failed, FileError{Path: rel, Err: err})
			continue
		}
		if err := writeRestored(dest, f.Content); err != nil {
			res.Failed = append(res.Failed, FileError{Path: rel, Err: err})
			continue
		}
		res.Written++
	}
	return res, nil
}

// restoredMode picks the permissions a restored file ends at: an existing
// destination keeps what it has, a new one gets 0o644, and generated content is
// forced read-only whichever it was. Without the last rule a freshly pulled
// generated view landed 0o644 and silently lost the protection that exists to
// stop hand edits.
func restoredMode(dest string, content []byte) os.FileMode {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(dest); err == nil {
		mode = fi.Mode().Perm()
	}
	if isGenerated(content) {
		mode = generatedViewMode
	}
	return mode
}

// generatedViewMode mirrors internal/docindex's okfViewMode: a generated view is
// written read-only so nobody hand-edits it.
const generatedViewMode = os.FileMode(0o444)

// writeRestored replaces dest with content at the mode restoredMode chose.
//
// It REMOVES the destination first rather than writing over it. A 0o444 view
// cannot be opened O_WRONLY even by its owner, so the plain WriteFile this
// replaced failed with EACCES against satelle's own generated views — satelle's
// read-only protection blocking satelle's own writer. The remedy is the one
// internal/docindex already uses for exactly this case (okf.go: `_ = os.Remove(path)
// // the existing view may be read-only`): that mode exists to stop HAND edits,
// and Restore is satelle's writer, so it gets the same privilege. Skipping
// marker-carrying files instead would leave the pull permanently unable to
// converge generated content — divergence, silently.
//
// The explicit Chmod after the write is required, not redundant: WriteFile's
// perm argument is masked by the umask on create, so 0o444 would otherwise land
// as whatever the operator's umask allows.
func writeRestored(dest string, content []byte) error {
	mode := restoredMode(dest, content)
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(dest, content, mode); err != nil {
		return err
	}
	return os.Chmod(dest, mode)
}

// isGenerated reports whether content carries the `generated: satelle` marker in
// its leading frontmatter block. The marker's spelling is owned by
// internal/docindex (okfGeneratedKey / okfGeneratedVal); it is re-scanned here
// rather than imported because this package is pure filesystem mechanism and
// must not depend on the doc index.
func isGenerated(content []byte) bool {
	s := string(content)
	if !strings.HasPrefix(s, "---\n") {
		return false
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return false
	}
	for _, line := range strings.Split(s[4:4+end], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) == "generated" && strings.TrimSpace(v) == "satelle" {
			return true
		}
	}
	return false
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
