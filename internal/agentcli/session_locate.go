package agentcli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// A harness keys its session record by the tree the session was started in, but
// the tree satelle asks about is the story's — a worktree, or the main tree a
// subagent's parent started in. locateSessionRecord is the one place every
// tree-keyed reader (claude, grok, pi) resolves a session id to its record, so
// the search order and the unavailable reasons cannot drift between adapters:
// the story's own tree, then each hint root the caller knows the session by, then
// a store-wide search for the id.
//
// base is the harness store directory holding one sub-directory per tree;
// dirName maps a tree root to that sub-directory's name; pattern is the record's
// name inside it (glob syntax, so it may carry the id inside a longer name).
// On success it returns the record's path; otherwise a reason naming adapter and
// the case — not found (with the trees tried) or ambiguous record.
func locateSessionRecord(adapter, sessionID, base string, dirName func(root string) string, pattern string, roots []string) (path, reason string) {
	var tried []string
	seen := map[string]bool{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		dir := filepath.Join(base, dirName(root))
		if seen[dir] {
			continue
		}
		seen[dir] = true
		tried = append(tried, dir)
		if matches, _ := filepath.Glob(filepath.Join(dir, pattern)); len(matches) > 0 {
			sort.Strings(matches)
			return matches[len(matches)-1], ""
		}
	}
	wide, _ := filepath.Glob(filepath.Join(base, "*", pattern))
	var found []string
	for _, m := range wide {
		if !seen[filepath.Dir(m)] {
			found = append(found, m)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Sprintf("%s: session record for %s not found under %s; store-wide search of %s found none",
			adapter, sessionID, strings.Join(tried, ", "), base)
	case 1:
		return found[0], ""
	default:
		sort.Strings(found)
		return "", fmt.Sprintf("%s: ambiguous session record for %s: found in %d trees (%s) and not under %s",
			adapter, sessionID, len(found), strings.Join(found, ", "), strings.Join(tried, ", "))
	}
}
