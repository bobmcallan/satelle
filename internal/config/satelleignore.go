package config

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// IgnoreFileName is the file, directly under the data dir, whose gitignore-style
// patterns name paths that never travel as synced or published substrate.
const IgnoreFileName = ".satelleignore"

// defaultIgnorePatterns are always active, ahead of the file's patterns, and no
// "!" line lifts them: the remote's secrets live in config/.env and reach the
// hosted server only as hidden-value secrets, never as substrate.
var defaultIgnorePatterns = []string{"config/.env"}

// Ignore decides which data-dir-relative paths a sync or publish must leave out.
// The zero Ignore applies the default patterns only, which is what a repo with
// no .satelleignore gets. It holds no state beyond the parsed file, so each
// command loads it once (LoadIgnore) and passes it where it is needed.
type Ignore struct {
	rules []ignoreRule
}

type ignoreRule struct {
	glob     string // pattern less "!", a leading "/" and a trailing "/"
	negate   bool
	dirOnly  bool // matches a directory only (a trailing "/")
	anchored bool // matches from the data dir, not at any depth
}

func (r ignoreRule) matches(p string) bool {
	if r.anchored {
		return MatchSegments(strings.Split(r.glob, "/"), strings.Split(p, "/"))
	}
	return GlobMatch(r.glob, p)
}

// LoadIgnore reads <dataDir>/.satelleignore. A missing file yields the default
// patterns alone; one that exists but cannot be read is an error, never a
// silently empty list that would let an excluded path through.
func LoadIgnore(dataDir string) (Ignore, error) {
	f, err := os.Open(filepath.Join(dataDir, IgnoreFileName))
	if os.IsNotExist(err) {
		return Ignore{}, nil
	}
	if err != nil {
		return Ignore{}, fmt.Errorf("read %s: %w", IgnoreFileName, err)
	}
	defer f.Close()
	var ign Ignore
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if r, ok := parseIgnoreLine(sc.Text()); ok {
			ign.rules = append(ign.rules, r)
		}
	}
	if err := sc.Err(); err != nil {
		return Ignore{}, fmt.Errorf("read %s: %w", IgnoreFileName, err)
	}
	return ign, nil
}

// ParseIgnore builds an Ignore from the text of a .satelleignore file.
func ParseIgnore(text string) Ignore {
	var ign Ignore
	for _, line := range strings.Split(text, "\n") {
		if r, ok := parseIgnoreLine(line); ok {
			ign.rules = append(ign.rules, r)
		}
	}
	return ign
}

func parseIgnoreLine(line string) (ignoreRule, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ignoreRule{}, false
	}
	var r ignoreRule
	if strings.HasPrefix(line, "!") {
		r.negate = true
		line = line[1:]
	} else if strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`) {
		line = line[1:] // an escaped leading "#" or "!" is a literal
	}
	if strings.HasSuffix(line, "/") {
		r.dirOnly = true
		line = strings.TrimRight(line, "/")
	}
	// A pattern with no "/" left matches at any depth; one with a "/" anywhere
	// (leading included) is anchored to the data dir.
	r.anchored = strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return ignoreRule{}, false
	}
	r.glob = line
	return r, true
}

// Match reports whether rel (relative to the data dir) is ignored. A path is
// ignored when it, or any directory above it, is — and as in git, a "!" line
// cannot bring back a path under an ignored directory. rel is cleaned first, so
// "config/./.env", "config//.env" and "workflows/../config/.env" are judged as
// the "config/.env" they name, not as the spelling a caller typed.
func (ign Ignore) Match(rel string) bool {
	rel = strings.TrimLeft(path.Clean(filepath.ToSlash(rel)), "/")
	if rel == "" || rel == "." {
		return false
	}
	for _, d := range defaultIgnorePatterns {
		if GlobMatch(d, rel) {
			return true
		}
	}
	segs := strings.Split(rel, "/")
	for i := 1; i <= len(segs); i++ {
		if ign.ignored(strings.Join(segs[:i], "/"), i < len(segs)) {
			return true
		}
	}
	return false
}

// ignored applies the file's rules to one path (a file, or a directory when
// isDir): the last rule that matches decides.
func (ign Ignore) ignored(p string, isDir bool) bool {
	out := false
	for _, r := range ign.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if r.matches(p) {
			out = !r.negate
		}
	}
	return out
}
