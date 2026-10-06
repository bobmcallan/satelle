package docindex

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// DirState is the three-valued state of an authored directory. "Absent" and
// "unreadable" are different facts with different consequences: an absent dir is
// a fresh or ungoverned corner of a repo, while an unreadable one is an authored
// process that EXISTS and cannot be read — which must never be mistaken for the
// first and silently replaced by an embedded default (sty_d6e209aa).
type DirState int

const (
	// DirAbsent: the path does not exist (or is not configured).
	DirAbsent DirState = iota
	// DirReadable: the path is a directory that can be listed.
	DirReadable
	// DirUnreadable: the path exists but cannot be listed or read — a stat or
	// readdir error other than "does not exist", or it is not a directory.
	DirUnreadable
)

// ProbeDir is the ONE definition of absent versus unreadable for an authored
// dir. Every loader of the authored tree (the store-backed walk and doctor's
// store-free read) asks it, so two loaders cannot disagree about the same dir.
func ProbeDir(dir string) (DirState, error) {
	if strings.TrimSpace(dir) == "" {
		return DirAbsent, nil
	}
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return DirAbsent, nil
	}
	if err != nil {
		return DirUnreadable, err
	}
	if !info.IsDir() {
		return DirUnreadable, fmt.Errorf("not a directory")
	}
	if _, err := os.ReadDir(dir); err != nil {
		return DirUnreadable, err
	}
	return DirReadable, nil
}

// ReadError marks a failure to read authored content that exists — a file or a
// subdirectory — as distinct from an index (database) failure. Sync wraps it so
// List can tell "the authored process cannot be read" from "the index hiccupped".
type ReadError struct {
	Path string
	Err  error
}

func (e *ReadError) Error() string { return e.Err.Error() } // the wrapper's own prefix names the path
func (e *ReadError) Unwrap() error { return e.Err }

// UnreadableSentinel is the Name of the marker doc a loader returns, in place of
// the dir's contents, when the dir is unreadable. A doc list is the one thing
// every consumer already passes around, so carrying the state in it reaches the
// precedence rule through both loaders without changing a signature.
const UnreadableSentinel = "__unreadable__"

// UnreadableDoc builds the sentinel for kind's dir. Path is the dir, Unreadable
// the reason; no loader may also return the kind's embedded defaults beside it.
func UnreadableDoc(kind, dir string, err error) Doc {
	reason := "unreadable"
	if err != nil {
		reason = err.Error()
	}
	return Doc{Kind: kind, Name: UnreadableSentinel, Path: dir, Unreadable: reason}
}

// IsUnreadable reports whether d is the unreadable-dir sentinel.
func (d Doc) IsUnreadable() bool { return d.Name == UnreadableSentinel && d.Unreadable != "" }

// UnreadableOf returns the first unreadable-dir sentinel in docs.
func UnreadableOf(docs []Doc) (path, reason string, ok bool) {
	for _, d := range docs {
		if d.IsUnreadable() {
			return d.Path, d.Unreadable, true
		}
	}
	return "", "", false
}

// WithoutUnreadable returns docs minus any sentinel, for the consumers that
// iterate rows (a sentinel is a state, never a row).
func WithoutUnreadable(docs []Doc) []Doc {
	out := make([]Doc, 0, len(docs))
	for _, d := range docs {
		if !d.IsUnreadable() {
			out = append(out, d)
		}
	}
	return out
}
