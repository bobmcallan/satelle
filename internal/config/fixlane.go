// In-loop fix lane bound (sty_4b694872). The lane changes WHO MAY EDIT, never
// WHAT IS JUDGED, and its bound is repo configuration: an embedded default
// (substrate/config/fix_lane.toml) a repo layers its own [fix_lane] table on.
// No bound figure and no path class is a Go literal, and the substrate path
// classes are DERIVED from where this repo actually keeps its substrate
// (data_dir, [substrate_roots]) — never from a fixed ".satelle/" spelling.
package config

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// FixLaneConfig is the [fix_lane] table.
//
//	[fix_lane]
//	max_lines       = <changed lines>
//	product_surface = ["cmd/**", "internal/**"]
//	gate_skills     = ["extra/skills/**"]
type FixLaneConfig struct {
	// MaxLines is the largest size bound, in changed lines, a claim may declare.
	// Zero or negative means unset: the embedded default applies.
	MaxLines int `toml:"max_lines"`
	// ProductSurface are the path globs the lane may never touch because they
	// are the shipped product. Repo-declared only; empty means the lane is
	// closed (fail-closed), not open.
	ProductSurface []string `toml:"product_surface"`
	// GateSkills, ReviewerRubrics, Workflows, Principles and RepoConfig are the
	// refused substrate classes. In a config file they are ADDITIONS: ResolveFixLane
	// unions them with the classes it derives from the repo's real substrate
	// locations, so a repo can add to a refused class and never subtract.
	GateSkills      []string `toml:"gate_skills"`
	ReviewerRubrics []string `toml:"reviewer_rubrics"`
	Workflows       []string `toml:"workflows"`
	Principles      []string `toml:"principles"`
	RepoConfig      []string `toml:"repo_config"`
}

const fixLaneFile = "substrate/config/fix_lane.toml"

var (
	embeddedFixLaneOnce sync.Once
	embeddedFixLane     FixLaneConfig
	embeddedFixLaneErr  error
)

// EmbeddedFixLane returns the embedded default [fix_lane] table, parsed once.
// It is the zero value on a parse failure (EmbeddedFixLaneErr reports it), and
// the zero value has MaxLines 0 — a closed lane, never an open one.
func EmbeddedFixLane() FixLaneConfig {
	embeddedFixLaneOnce.Do(func() {
		raw, err := substrateFS.ReadFile(fixLaneFile)
		if err != nil {
			embeddedFixLaneErr = err
			return
		}
		var file struct {
			FixLane FixLaneConfig `toml:"fix_lane"`
		}
		if _, err := toml.Decode(string(raw), &file); err != nil {
			embeddedFixLaneErr = err
			return
		}
		embeddedFixLane = file.FixLane
	})
	return embeddedFixLane
}

// EmbeddedFixLaneErr is non-nil when the embedded default failed to load.
func EmbeddedFixLaneErr() error {
	_ = EmbeddedFixLane()
	return embeddedFixLaneErr
}

// ResolveFixLane merges the repo's [fix_lane] over the embedded default and
// derives the refused substrate classes from where THIS repo keeps its
// substrate under repoRoot: MaxLines is the repo's when set, else the default's;
// ProductSurface is the repo's alone; each refused class is the union of the
// embedded list, the repo's list and the locations satelle actually resolves
// (see substrateClasses). A repo that relocates data_dir or a substrate root
// therefore keeps every refusal.
func (c Config) ResolveFixLane(repoRoot string) FixLaneConfig {
	def := EmbeddedFixLane()
	derived := c.substrateClasses(repoRoot)
	out := FixLaneConfig{
		MaxLines:        def.MaxLines,
		ProductSurface:  cleanGlobs(c.FixLane.ProductSurface),
		GateSkills:      unionGlobs(def.GateSkills, derived.GateSkills, c.FixLane.GateSkills),
		ReviewerRubrics: unionGlobs(def.ReviewerRubrics, derived.ReviewerRubrics, c.FixLane.ReviewerRubrics),
		Workflows:       unionGlobs(def.Workflows, derived.Workflows, c.FixLane.Workflows),
		Principles:      unionGlobs(def.Principles, derived.Principles, c.FixLane.Principles),
		RepoConfig:      unionGlobs(def.RepoConfig, derived.RepoConfig, c.FixLane.RepoConfig),
	}
	if c.FixLane.MaxLines > 0 {
		out.MaxLines = c.FixLane.MaxLines
	}
	return out
}

// substrateClasses derives the refused classes from the resolved locations:
//
//   - gate skills     — the skills root (every skill is some gate's rubric text)
//   - workflows       — the workflows root
//   - principles      — the principles root and the constitution file
//   - reviewer rubric — the agents layer that binds which rubric and grant a
//     reviewer runs with: workflows/agents.toml, the workspace layer, the legacy
//     agents.toml/actors.toml at the data dir, and agents.toml in a relocated
//     workflows root
//   - repo config     — satelle.toml and satelle.local.toml, which carry this
//     very bound: a lane that could edit its own [fix_lane] would have none
//
// A location outside repoRoot is skipped: such a path is refused as
// outside-repo before any class is consulted.
func (c Config) substrateClasses(repoRoot string) FixLaneConfig {
	dirs := c.ResolveAuthoredDirs(repoRoot)
	data := c.ResolveDataDir(repoRoot)
	tree := func(abs string) []string {
		if rel, ok := relUnder(repoRoot, abs); ok {
			return []string{rel + "/**"}
		}
		return nil
	}
	file := func(abs ...string) []string {
		var out []string
		for _, a := range abs {
			if rel, ok := relUnder(repoRoot, a); ok {
				out = append(out, rel)
			}
		}
		return out
	}
	return FixLaneConfig{
		GateSkills: tree(dirs["skills"]),
		Workflows:  tree(dirs["workflows"]),
		Principles: append(tree(dirs["principles"]), file(c.ResolveConstitution(repoRoot))...),
		ReviewerRubrics: file(
			filepath.Join(data, AgentsConfigDir, AgentsConfigName),
			filepath.Join(data, AgentsConfigDir, WorkspaceAgentsName),
			filepath.Join(data, AgentsConfigDir, ActorsConfigName),
			filepath.Join(data, AgentsConfigName),
			filepath.Join(data, ActorsConfigName),
			filepath.Join(dirs["workflows"], AgentsConfigName),
		),
		RepoConfig: file(filepath.Join(data, ConfigName), filepath.Join(data, LocalConfigName)),
	}
}

// relUnder returns abs as a slash-separated path relative to repoRoot, with
// glob metacharacters escaped so a directory named "a[b" matches itself and
// nothing else. ok is false when abs is blank or not inside repoRoot.
func relUnder(repoRoot, abs string) (string, bool) {
	if strings.TrimSpace(abs) == "" {
		return "", false
	}
	if strings.TrimSpace(repoRoot) == "" {
		repoRoot = "."
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	rel, err := filepath.Rel(root, filepath.Clean(abs))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return escapeGlob(filepath.ToSlash(rel)), true
}

func escapeGlob(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '*', '?', '[', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// cleanGlobs drops blank entries — a blank glob must never match everything.
func cleanGlobs(in []string) []string {
	var out []string
	for _, g := range in {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

func unionGlobs(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, g := range cleanGlobs(l) {
			if !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	return out
}
