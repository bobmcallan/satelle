package config

import (
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

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
// are returned as written (repo-root-relative or absolute); see
// ResolveLockSubstratePaths for the resolved form.
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

// ResolveLockSubstratePaths reads the committed config at configPath and returns
// the substrate lock roots resolved against repoRoot (the same join
// ResolveEditExemptPaths applies) plus whether the operator explicitly opted
// out. An unreadable file fails closed to the default lock.
func ResolveLockSubstratePaths(configPath, repoRoot string) (roots []string, explicitOptOut bool) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		raw = nil
	}
	paths, optOut := ParseLockSubstratePaths(string(raw))
	for _, p := range paths {
		roots = append(roots, resolveUnder(repoRoot, p))
	}
	return roots, optOut
}
