package config

import (
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// AgentsLayerPathPattern is the agents layer as a repo-relative path: the
// agents*.toml bindings directly under .satelle/workflows/. It is the whole
// surface of the agents-repair lane — the substrate lock admits that lane's
// story for these paths alone, and the lane's close check
// (satelle-agents-layer-only-check, which carries this exact string) admits no
// other path.
const AgentsLayerPathPattern = `^\.satelle/workflows/agents[^/]*\.toml$`

var agentsLayerPath = regexp.MustCompile(AgentsLayerPathPattern)

// IsAgentsLayerPath reports whether rel, a slash-separated path relative to the
// repo root, is an agents-layer binding file.
func IsAgentsLayerPath(rel string) bool {
	return agentsLayerPath.MatchString(rel)
}

// DefaultLockSubstratePaths is the substrate lock list a repo gets when its
// committed satelle.toml does not mention [gate] lock_substrate_paths at all:
// the data dir. The lock is the default posture, not an opt-in (sty_992cffc6).
var DefaultLockSubstratePaths = []string{".satelle/"}

// ParseLockSubstratePaths decides the substrate lock list from the raw bytes of
// the committed satelle.toml. It keys off key PRESENCE, which the decoded
// Config cannot report (an absent key and `= []` both decode to an empty
// slice), so the three postures stay distinct:
//
//	key absent under [gate]      -> DefaultLockSubstratePaths, optOut false
//	key present with entries     -> those entries (blanks trimmed), optOut false
//	key present, [] or all blank -> nil, optOut true (the documented opt-out)
//
// Anything the parser cannot read with confidence — a TOML syntax error, a
// value that is not a list of strings — fails CLOSED to the default lock, never
// to permissive: a damaged file must not read as an operator's opt-out. Entries
// are returned as written (repo-root-relative or absolute).
func ParseLockSubstratePaths(content string) (paths []string, optOut bool) {
	var probe struct {
		Gate struct {
			LockSubstratePaths []string `toml:"lock_substrate_paths"`
		} `toml:"gate"`
	}
	md, err := toml.Decode(content, &probe)
	if err != nil || !md.IsDefined("gate", "lock_substrate_paths") {
		return append([]string{}, DefaultLockSubstratePaths...), false
	}
	for _, p := range probe.Gate.LockSubstratePaths {
		if s := strings.TrimSpace(p); s != "" {
			paths = append(paths, s)
		}
	}
	if len(paths) == 0 {
		return nil, true
	}
	return paths, false
}
