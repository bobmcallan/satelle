package config

import (
	"path"
	"strings"
)

// GlobMatch reports whether rel (slash-separated, relative) matches glob. A
// glob without a "/" matches a basename at any depth; otherwise it is matched
// segment by segment against the path, "**" standing for any run of segments
// (including none). Blank and malformed globs never match — a bad glob must
// not fail open toward matching everything. It is the one segment matcher:
// the fix lane's bound and the .satelleignore layer both build on it.
func GlobMatch(glob, rel string) bool {
	glob = strings.TrimSpace(glob)
	if glob == "" || rel == "" {
		return false
	}
	if !strings.Contains(glob, "/") {
		ok, err := path.Match(glob, path.Base(rel))
		return err == nil && ok
	}
	return MatchSegments(strings.Split(glob, "/"), strings.Split(rel, "/"))
}

// MatchSegments matches pat against segs segment by segment with path.Match,
// "**" standing for any run of segments (including none).
func MatchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			if len(rest) == 0 {
				return len(segs) > 0 // "dir/**" needs something beneath dir
			}
			for i := 0; i <= len(segs); i++ {
				if MatchSegments(rest, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], segs[0])
		if err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}
