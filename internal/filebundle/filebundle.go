// Package filebundle is the one owner of satelle's versioned file-bundle
// encoding and of the safe restore of a bundle onto disk. A bundle is a list of
// (path, text) entries framed as JSON, so a body containing any markup — a
// "### " heading included — round-trips byte for byte.
//
// It is remote-agnostic: the caller decides which files make a bundle and in
// what order, and which roots a decoded path must sit under. Encode keeps the
// order it is given and does no sorting of its own, so the same entries in the
// same order always yield the same bytes.
package filebundle

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/bobmcallan/satelle/internal/config"
)

// Format is the version of the bundle encoding. Decode refuses any other.
const Format = 1

// Entry is one file of a bundle: a slash path relative to the bundle root and
// its text.
type Entry struct {
	Path    string
	Content string
}

type bundle struct {
	Format int    `json:"format"`
	Files  []file `json:"files"`
}

type file struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ErrMalformed is matched (errors.Is) by the error Decode returns for data that
// is not a bundle at all.
var ErrMalformed = errors.New("malformed bundle")

// MalformedError is Decode's error for data that is not a bundle; Err is the
// JSON cause.
type MalformedError struct{ Err error }

func (e *MalformedError) Error() string        { return e.Err.Error() }
func (e *MalformedError) Unwrap() error        { return e.Err }
func (e *MalformedError) Is(target error) bool { return target == ErrMalformed }

// FormatError is Decode's error for a bundle of a format this build does not read.
type FormatError struct{ Got, Want int }

func (e *FormatError) Error() string {
	return fmt.Sprintf("unknown bundle format %d (this satelle reads %d)", e.Got, e.Want)
}

// Encode frames entries as one bundle, in the order given. A .local path never
// enters a bundle (config.LocalOnlyPath owns "never transmitted"), and content
// that is not UTF-8 is refused: a bundle carries text.
func Encode(entries []Entry) ([]byte, error) {
	b := bundle{Format: Format, Files: make([]file, 0, len(entries))}
	for _, e := range entries {
		if config.LocalOnlyPath(e.Path) {
			return nil, fmt.Errorf("%s: a .local file never enters a bundle", e.Path)
		}
		if !utf8.ValidString(e.Content) {
			return nil, fmt.Errorf("%s: not UTF-8 text", e.Path)
		}
		b.Files = append(b.Files, file{Path: e.Path, Content: e.Content})
	}
	return json.Marshal(b)
}

// Decode reads a bundle and checks every entry path against roots (CheckPath)
// before returning any of them.
func Decode(data []byte, roots []string) ([]Entry, error) {
	var b bundle
	if !utf8.Valid(data) {
		// json.Unmarshal would silently replace the bad bytes; a bundle is text.
		return nil, &MalformedError{Err: errors.New("content is not UTF-8 text")}
	}
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, &MalformedError{Err: err}
	}
	if b.Format != Format {
		return nil, &FormatError{Got: b.Format, Want: Format}
	}
	out := make([]Entry, 0, len(b.Files))
	for _, f := range b.Files {
		if err := CheckPath(f.Path, roots); err != nil {
			return nil, err
		}
		out = append(out, Entry{Path: f.Path, Content: f.Content})
	}
	return out, nil
}

// CheckPath refuses a path that is not a plain relative slash path inside the
// bundle: empty, absolute, containing a backslash, not clean, escaping with
// "..", a .local file, or (when roots is non-empty) outside every root. A root
// matches a path equal to it or under it.
func CheckPath(p string, roots []string) error {
	if p == "" || path.IsAbs(p) || strings.Contains(p, "\\") || path.Clean(p) != p {
		return fmt.Errorf("bundle path %q is not a plain relative path", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("bundle path %q escapes the bundle directory", p)
		}
	}
	if config.LocalOnlyPath(p) {
		return fmt.Errorf("bundle path %q is a .local file, which never travels", p)
	}
	if len(roots) == 0 {
		return nil
	}
	for _, root := range roots {
		if p == root || strings.HasPrefix(p, root+"/") {
			return nil
		}
	}
	return fmt.Errorf("bundle path %q is outside the bundle's roots", p)
}

// Write writes entries into dir, byte for byte. Every path is checked (generic
// checks only; the caller already ran Decode with its roots) before anything is
// written. A non-empty dir is refused unless overwrite is set. With overwrite,
// every file of dir that owned reports true and the entries do not carry is
// removed first; a .local file is never removed. owned may be nil, which owns
// nothing.
func Write(dir string, entries []Entry, overwrite bool, owned func(rel string) bool) error {
	keep := map[string]bool{}
	for _, e := range entries {
		if err := CheckPath(e.Path, nil); err != nil {
			return err
		}
		keep[e.Path] = true
	}
	if existing, err := os.ReadDir(dir); err == nil && len(existing) > 0 && !overwrite {
		return fmt.Errorf("%s is not empty — pass --force to overwrite it", dir)
	}
	if overwrite && owned != nil {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if keep[rel] || config.LocalOnlyPath(rel) || !owned(rel) {
				return nil
			}
			return os.Remove(p)
		})
		if err != nil {
			return err
		}
	}
	for _, e := range entries {
		dest := filepath.Join(dir, filepath.FromSlash(e.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, []byte(e.Content), 0o644); err != nil {
			return err
		}
	}
	return nil
}
