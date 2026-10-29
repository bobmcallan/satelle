package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// TeamConfig is the [team] table: the .satelle paths a bound repo's project
// owns (sty_8dedc0a4). Each entry is a glob relative to the data dir, the same
// key the hosted store files a path under ("skills/**", "scripts/*.sh"). A
// `**` segment matches any depth; other segments follow path.Match.
//
// Resolution reads the table from the project's DEPLOYED satelle.toml — the
// source declares the set — with satelle.local.toml's [team] above it. The
// repo's own satelle.toml [team] is what a push from that repo publishes.
type TeamConfig struct {
	Paths []string `toml:"paths"`
}

// DefaultTeamPaths is the team-owned set when no [team] paths is declared.
var DefaultTeamPaths = []string{AgentsRel, "workflows/**", "skills/**"}

// TeamSet is a normalised team-owned set. The zero value owns nothing.
type TeamSet struct {
	globs []string
}

// NewTeamSet normalises globs into a TeamSet. nil means DefaultTeamPaths; an
// empty, non-nil slice owns nothing.
func NewTeamSet(globs []string) TeamSet {
	if globs == nil {
		globs = DefaultTeamPaths
	}
	out := make([]string, 0, len(globs))
	for _, g := range globs {
		if n := normaliseTeamGlob(g); n != "" {
			out = append(out, n)
		}
	}
	return TeamSet{globs: out}
}

// Globs is the set's normalised globs.
func (t TeamSet) Globs() []string { return append([]string(nil), t.globs...) }

// Owns reports whether the data-dir-relative slash path rel is team-owned: a
// glob matches it and it is none of the paths that can never be: a `.local`
// file, the settings file, the synced workspace agents layer, or a file
// `satelle init` writes.
func (t TeamSet) Owns(rel string) bool {
	rel = normaliseTeamGlob(rel)
	if rel == "" || LocalOnlyPath(rel) || rel == ConfigName || rel == WorkspaceAgentsRel || InitOwnedPath(rel) {
		return false
	}
	return t.matches(rel)
}

// InitOwnedMatched lists the init-owned files a glob of the set names. They stay
// with init (Owns refuses them); doctor reports the exclusion.
func (t TeamSet) InitOwnedMatched() []string {
	var out []string
	for _, rel := range initOwnedRels() {
		if t.matches(rel) {
			out = append(out, rel)
		}
	}
	return out
}

func (t TeamSet) matches(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, g := range t.globs {
		if matchGlobSegs(strings.Split(g, "/"), parts) {
			return true
		}
	}
	return false
}

// matchGlobSegs matches a slash-split glob against a slash-split path: `**`
// spans zero or more segments, any other segment is a path.Match pattern.
func matchGlobSegs(glob, name []string) bool {
	for len(glob) > 0 {
		if glob[0] == "**" {
			rest := glob[1:]
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchGlobSegs(rest, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(glob[0], name[0]); err != nil || !ok {
			return false
		}
		glob, name = glob[1:], name[1:]
	}
	return len(name) == 0
}

// normaliseTeamGlob trims a glob or path to the data-dir-relative slash form:
// no leading "./", ".satelle/" or "/". The bare "agents.toml" names the agents
// layer where it lives (AgentsRel).
func normaliseTeamGlob(g string) string {
	g = filepath.ToSlash(strings.TrimSpace(g))
	for {
		next := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(g, "./"), DefaultDataDir+"/"), "/")
		if next == g {
			break
		}
		g = next
	}
	if g == AgentsConfigName {
		return AgentsRel
	}
	return g
}

// validateTeam refuses a [team] paths entry that is not a usable glob, so a typo
// fails at load rather than silently owning nothing.
func validateTeam(cfg Config, file string) error {
	for _, g := range cfg.Team.Paths {
		n := normaliseTeamGlob(g)
		if n == "" {
			return fmt.Errorf("config: %s: [team] paths: empty entry", file)
		}
		for _, seg := range strings.Split(n, "/") {
			if seg == ".." {
				return fmt.Errorf("config: %s: [team] paths %q: must stay under %s/", file, g, DefaultDataDir)
			}
			if _, err := path.Match(seg, ""); err != nil {
				return fmt.Errorf("config: %s: [team] paths %q: %w", file, g, err)
			}
		}
	}
	return nil
}

// InitOwnedPath reports whether a data-dir-relative path is one `satelle init`
// writes and heals (writeHookScripts, scaffold drift): the binary is its source,
// so no team glob can own it.
func InitOwnedPath(rel string) bool {
	rel = normaliseTeamGlob(rel)
	for _, r := range initOwnedRels() {
		if r == rel {
			return true
		}
	}
	if ok, _ := path.Match("hooks/pretooluse-*-*.sh", rel); ok {
		return true
	}
	return false
}

func initOwnedRels() []string {
	return []string{
		strings.TrimPrefix(HookWrapperRel, DefaultDataDir+"/"),
		"hooks/stop-kimi.sh",
		"bin/kimi-argv.sh",
		"kimi/config.toml",
	}
}

// DeployedTeamPaths reads [team] paths from a satelle.toml body. ok is false
// when the body declares none (or does not decode), so the default applies.
func DeployedTeamPaths(body []byte) (paths []string, ok bool) {
	var doc struct {
		Team TeamConfig `toml:"team"`
	}
	md, err := toml.Decode(string(body), &doc)
	if err != nil || !md.IsDefined("team", "paths") {
		return nil, false
	}
	if doc.Team.Paths == nil {
		return []string{}, true
	}
	return doc.Team.Paths, true
}

// LocalTeamPaths reads [team] paths from the satelle.local.toml in dataDir — the
// override that sits above the deployed declaration.
func LocalTeamPaths(dataDir string) ([]string, bool) {
	b, err := os.ReadFile(filepath.Join(dataDir, LocalConfigName))
	if err != nil {
		return nil, false
	}
	return DeployedTeamPaths(b)
}
