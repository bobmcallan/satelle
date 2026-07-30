// resolveagents.go — the deterministic precedence engine that folds the
// machine-wide profile catalog into a repo's agents layer (sty_c7dfeedf).
//
// The ladder, highest first:
//
//  1. repo    — an inline value on the binding in .satelle/agents.toml
//  2. profile — the catalog profile the binding EXPLICITLY names via profile=
//     (and, transitively, whatever that profile extends)
//  3. global-role — the catalog's [roles] default for the binding's role, and
//     ONLY when the repo opted in with [defaults] use_global_roles
//  4. embedded — the binary's safe fallback (DefaultExecutorCommand,
//     DefaultReviewerCommand, DefaultReviewerTools), applied as today
//     by the *Binding resolvers
//
// The critical negative: tier 2 and tier 3 are reached only by an explicit
// reference. A catalog profile named "reviewer" and a repo [reviewer] that never
// mentions it DO NOT merge — the repo resolves byte-identically with and without
// the catalog present. Every convenience that would blur this is deliberately
// absent, because a machine-wide file that can silently re-point a pinned repo's
// reviewer is exactly the failure this design exists to prevent.
//
// Resolution returns the same AgentsConfig shape everything downstream already
// consumes, plus a Provenance side-table, so no dispatch or gate code changes.
package config

import (
	"fmt"
	"sort"
	"strings"
)

// Source labels recorded in Provenance. Profile and role sources carry the
// profile name, so a display surface names WHICH profile a field came from.
const (
	SourceRepo     = "repo"
	SourceEmbedded = "embedded"
)

// SourceProfile labels a field won by an explicitly referenced catalog profile.
func SourceProfile(profile string) string { return "profile:" + profile }

// SourceGlobalRole labels a field won by the catalog's opt-in [roles] default.
func SourceGlobalRole(profile string) string { return "global-role:" + profile }

// Provenance records, per binding section, the source of every effective field.
// Keys are the TOML field names (command, tools, model, …) so a display surface
// can render "field value (source)" without re-deriving anything.
type Provenance map[string]map[string]string

// Source returns the recorded source for one field, or "" when the field was
// never set by any tier (no value, so nothing to attribute).
func (p Provenance) Source(section, field string) string {
	if p == nil {
		return ""
	}
	return p[section][field]
}

// Fields returns the sourced field names for a section, sorted — a stable
// iteration order for rendering and snapshot tests.
func (p Provenance) Fields(section string) []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p[section]))
	for k := range p[section] {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// EffectiveAgents is the resolved agents layer plus what a caller needs to
// explain and expand it: where each field came from, and the layered ${VAR} KV.
type EffectiveAgents struct {
	// Agents is the resolved layer — the same shape LoadAgents returns, so every
	// downstream consumer (dispatch, gates, validate) is unchanged.
	Agents AgentsConfig
	// Provenance is the per-field source table for display and validation.
	Provenance Provenance
	// Vars is the catalog's [vars] layered UNDER the repo's, repo keys winning.
	// Expansion still happens in memory at wiring time (ResolveAgentEnvs) — a
	// catalog secret is never written into a repository.
	Vars map[string]string
}

// LoadEffectiveAgents is the SINGLE resolution site: read the repo layer, read
// the machine-wide catalog, and fold them by the documented precedence. Every
// surface that needs the effective agents layer calls this rather than merging
// on its own — two independently-merging call sites is the defect this exists to
// prevent. repoVars is the repo's [vars] KV (may be nil).
func LoadEffectiveAgents(dataDir string, repoVars map[string]string) (EffectiveAgents, error) {
	repo, err := LoadAgents(dataDir)
	if err != nil {
		return EffectiveAgents{}, err
	}
	global, err := LoadGlobalAgents()
	if err != nil {
		return EffectiveAgents{}, err
	}
	return ResolveEffectiveAgents(repo, global, repoVars)
}

// ResolveEffectiveAgents folds an already-loaded repo layer and catalog. Split
// from LoadEffectiveAgents so tests and multi-repo fixtures can resolve without
// disk, and so the precedence logic has no I/O in it.
func ResolveEffectiveAgents(repo AgentsConfig, global GlobalAgentsConfig, repoVars map[string]string) (EffectiveAgents, error) {
	agents, prov, err := ResolveAgents(repo, global)
	if err != nil {
		return EffectiveAgents{}, err
	}
	return EffectiveAgents{Agents: agents, Provenance: prov, Vars: LayerVars(global.Vars, repoVars)}, nil
}

// LayerVars returns the ${NAME} KV with the machine-wide catalog as the base and
// the repo's own [vars] (including its gitignored satelle.local.toml overlay)
// winning per key — the same per-key overlay shape the repo already uses for its
// own layers. Neither map is mutated.
func LayerVars(global, repo map[string]string) map[string]string {
	if len(global) == 0 && len(repo) == 0 {
		return nil
	}
	out := make(map[string]string, len(global)+len(repo))
	for k, v := range global {
		out[k] = v
	}
	for k, v := range repo {
		out[k] = v
	}
	return out
}

// ResolveAgents folds the catalog into the repo layer and returns the effective
// agents config with its per-field provenance. A missing referenced profile, a
// reference cycle, or a repo/profile role conflict is an error — resolution
// never half-applies a broken reference.
func ResolveAgents(repo AgentsConfig, global GlobalAgentsConfig) (AgentsConfig, Provenance, error) {
	out := AgentsConfig{Defaults: repo.Defaults, Agents: map[string]AgentBinding{}}
	prov := Provenance{}
	useRoles := repo.Defaults.UseGlobalRoles

	resolve := func(section string, b AgentBinding) (AgentBinding, error) {
		merged, src, err := resolveBindingProfile(section, b, global, useRoles)
		if err != nil {
			return AgentBinding{}, err
		}
		markEmbedded(section, merged, src)
		prov[section] = src
		return merged, nil
	}

	var err error
	if out.Executor, err = resolve("executor", repo.Executor); err != nil {
		return AgentsConfig{}, nil, err
	}
	if out.Reviewer, err = resolve("reviewer", repo.Reviewer); err != nil {
		return AgentsConfig{}, nil, err
	}
	for _, name := range sortedKeys(repo.Agents) {
		b, rerr := resolve(name, repo.Agents[name])
		if rerr != nil {
			return AgentsConfig{}, nil, rerr
		}
		out.Agents[name] = b
	}
	return out, prov, nil
}

// resolveBindingProfile merges one binding against the catalog, returning the
// effective binding and its per-field sources.
func resolveBindingProfile(section string, repoB AgentBinding, global GlobalAgentsConfig, useRoles bool) (AgentBinding, map[string]string, error) {
	base := AgentBinding{}
	baseSrc := map[string]string{}

	if ref := strings.TrimSpace(repoB.Profile); ref != "" {
		chain, err := global.resolveChain(ref)
		if err != nil {
			return AgentBinding{}, nil, fmt.Errorf("%s [%s] profile: %w", AgentsConfigName, section, err)
		}
		base, baseSrc = foldChain(global, chain, SourceProfile)
	} else if useRoles {
		// Tier 3 — reached only because the repo wrote use_global_roles. Absent
		// that opt-in there is no path from the catalog to a binding that did not
		// name a profile, which is what keeps a same-name profile inert.
		role := ResolvedRole(section, repoB)
		if target := strings.TrimSpace(global.Roles[role]); target != "" {
			chain, err := global.resolveChain(target)
			if err != nil {
				return AgentBinding{}, nil, fmt.Errorf("%s [defaults] use_global_roles: [roles] %s: %w", AgentsConfigName, role, err)
			}
			base, baseSrc = foldChain(global, chain, SourceGlobalRole)
		}
	}

	// Role is IDENTITY, not an overridable field: a repo that declares a role
	// differing from the profile's is stating two incompatible contracts, so it
	// is refused rather than silently resolved in either direction. Restating the
	// same role is fine and common.
	repoRole := strings.ToLower(strings.TrimSpace(repoB.Role))
	baseRole := strings.ToLower(strings.TrimSpace(base.Role))
	if repoRole != "" && baseRole != "" && repoRole != baseRole {
		return AgentBinding{}, nil, fmt.Errorf(
			"%s [%s] declares role=%q but %s declares role=%q for the profile it references — role is identity and must agree",
			AgentsConfigName, section, repoB.Role, GlobalAgentsLabel, base.Role)
	}

	merged, src := overlayBinding(base, baseSrc, repoB, SourceRepo)
	merged.Profile = repoB.Profile
	return merged, src, nil
}

// foldChain folds a profile reference chain (outermost first) into one binding,
// deepest base first so the outermost profile wins — the same direction the repo
// then wins over the whole chain. label names the tier that reached the chain.
func foldChain(global GlobalAgentsConfig, chain []string, label func(string) string) (AgentBinding, map[string]string) {
	b := AgentBinding{}
	src := map[string]string{}
	for i := len(chain) - 1; i >= 0; i-- {
		name := chain[i]
		b, src = overlayBinding(b, src, global.Profiles[name], label(name))
	}
	return b, src
}

// overlayBinding lays top over base: a non-empty scalar on top wins; env and
// settings merge key-wise with top's keys winning. src starts from baseSrc and
// records topSrc for every field top actually supplied, so the returned table
// always describes the returned binding.
func overlayBinding(base AgentBinding, baseSrc map[string]string, top AgentBinding, topSrc string) (AgentBinding, map[string]string) {
	out := base
	src := make(map[string]string, len(baseSrc)+4)
	for k, v := range baseSrc {
		src[k] = v
	}
	setScalar := func(field string, dst *string, val string) {
		if strings.TrimSpace(val) == "" {
			return
		}
		*dst = val
		src[field] = topSrc
	}
	setScalar("interface", &out.Interface, top.Interface)
	setScalar("command", &out.Command, top.Command)
	setScalar("tools", &out.Tools, top.Tools)
	setScalar("model", &out.Model, top.Model)
	setScalar("role", &out.Role, top.Role)
	setScalar("principles", &out.Principles, top.Principles)
	setScalar("timeout", &out.Timeout, top.Timeout)
	setScalar("effort", &out.Effort, top.Effort)
	setScalar("secondary", &out.Secondary, top.Secondary)

	if len(top.Env) > 0 {
		out.Env = mergeStringMap(base.Env, top.Env)
		src["env"] = mapSource(len(base.Env) > 0, topSrc, baseSrc["env"])
	}
	if len(top.Settings) > 0 {
		out.Settings = mergeAnyMap(base.Settings, top.Settings)
		src["settings"] = mapSource(len(base.Settings) > 0, topSrc, baseSrc["settings"])
	}
	return out, src
}

// mapSource labels a key-wise merged map field. When both tiers contributed keys
// the label names both, because attributing the whole table to the winner would
// hide that some keys came from underneath.
func mapSource(baseContributed bool, topSrc, baseSrc string) string {
	if baseContributed && baseSrc != "" && baseSrc != topSrc {
		return topSrc + "+" + baseSrc
	}
	return topSrc
}

// markEmbedded records the embedded fallback as the source for fields no tier
// supplied but that the *Binding resolvers fill from the binary's defaults, so
// a display surface attributes them honestly rather than showing them unsourced.
// It reports only — the resolvers stay the single owner of the actual defaulting.
func markEmbedded(section string, b AgentBinding, src map[string]string) {
	if strings.TrimSpace(b.Command) == "" {
		src["command"] = SourceEmbedded
	}
	// Only [reviewer] gets an embedded tools grant (DefaultReviewerTools); named
	// bindings resolve a command but keep an empty grant.
	if section == "reviewer" && strings.TrimSpace(b.Tools) == "" {
		src["tools"] = SourceEmbedded
	}
}

func mergeStringMap(base, top map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(top))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range top {
		out[k] = v
	}
	return out
}

func mergeAnyMap(base, top map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(top))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range top {
		out[k] = v
	}
	return out
}
