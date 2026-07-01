package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// AgentsConfigName is the per-repo agents-binding file, beside satelle.toml under
// the data dir (.satelle/agents.toml). ActorsConfigName is the now-removed legacy
// filename — it is no longer loaded (sty_7db2ed7d); `satelle reindex` warns a repo
// still carrying it so the rename is enforced rather than silently honoured.
const (
	AgentsConfigName = "agents.toml"
	ActorsConfigName = "actors.toml"
)

// Default agent grants — TODAY's behaviour, so an absent agents.toml changes
// nothing: the executor drives in-loop (the agent itself); the reviewer runs as
// an isolated agent with a READ-ONLY tool grant (see the
// satelle-agent-model principle — the reviewer is limited to reviewing).
const (
	DefaultExecutorHarness = "in-loop"
	// DefaultReviewerHarness is the bare claude PRESET name — a single token, so
	// agentcli.RunnerFromHarness expands it to the built-in claude template
	// (which carries the read-only --disallowedTools denylist). A repo overrides
	// it with a full command template (multi-token) in agents.toml.
	DefaultReviewerHarness = "claude"
	DefaultReviewerTools   = "Read,Grep,Glob"
)

// AgentBinding binds one agent to a backend (how/where it runs) and its grant
// (the tool allowance, and an optional model). Empty fields take the defaults.
//
// InjectPrinciples toggles whether an ISOLATED agent receives the session
// (principles:session) principles in its system prompt — the same guardrails the
// SessionStart injector gives the in-loop session (sty_46a40208). It DEFAULTS ON:
// a nil pointer (the field absent from agents.toml) means inject. Set
// inject_principles = false to omit them for that agent.
type AgentBinding struct {
	Harness          string `toml:"harness"`
	Tools            string `toml:"tools"`
	Model            string `toml:"model"`
	InjectPrinciples *bool  `toml:"inject_principles"`
}

// InjectsPrinciples reports whether this binding injects the resident principles
// into the isolated agent's context — true (the default) unless explicitly
// disabled with inject_principles = false.
func (b AgentBinding) InjectsPrinciples() bool {
	return b.InjectPrinciples == nil || *b.InjectPrinciples
}

// AgentsConfig is the on-disk shape at .satelle/agents.toml — the agents layer.
// Every field is optional; the *Binding resolvers supply today's defaults, so
// the zero value (and an absent file) is the current behaviour. Agents holds
// OPTIONAL named agents (beyond the executor/reviewer roles) declared as flat
// top-level [<name>] sections — consistent with [executor]/[reviewer] — or the
// legacy nested [agents.<name>] (still read for back-compat). A workflow node may
// allocate a step to one, and a named agent is ALWAYS isolated (see
// satelle-agent-model). LoadAgents does the classification; the toml tag here is
// retained only for the legacy nested form.
type AgentsConfig struct {
	Executor AgentBinding            `toml:"executor"`
	Reviewer AgentBinding            `toml:"reviewer"`
	Agents   map[string]AgentBinding `toml:"agents"`
}

// NamedBinding resolves an optional named agent declared as a flat top-level
// [<name>] section (or the legacy nested [agents.<name>]). ok is false when none is
// declared, so a workflow node that allocates a step to an absent agent degrades
// gracefully to the in-loop executor. A named agent is always isolated; an unset
// harness defaults to the isolated claude preset.
func (a AgentsConfig) NamedBinding(name string) (AgentBinding, bool) {
	b, ok := a.Agents[name]
	if !ok {
		return AgentBinding{}, false
	}
	if b.Harness == "" {
		b.Harness = DefaultReviewerHarness
	}
	return b, true
}

// ReviewerBinding resolves the reviewer agent's backend and grant, defaulting to
// an isolated agent with the read-only tool grant. The grant travels with the
// binding, so the reviewer's read-only limit holds whatever the backend.
func (a AgentsConfig) ReviewerBinding() AgentBinding {
	b := a.Reviewer
	if b.Harness == "" {
		b.Harness = DefaultReviewerHarness
	}
	if b.Tools == "" {
		b.Tools = DefaultReviewerTools
	}
	return b
}

// ExecutorBinding resolves the executor agent's backend, defaulting to in-loop
// (the driving agent itself).
func (a AgentsConfig) ExecutorBinding() AgentBinding {
	b := a.Executor
	if b.Harness == "" {
		b.Harness = DefaultExecutorHarness
	}
	return b
}

// LoadAgents reads the agents layer from <dataDir>/agents.toml. The legacy
// actors.toml is no longer read (sty_7db2ed7d); an absent agents.toml yields the
// zero AgentsConfig — defaults via the *Binding resolvers — and a nil error, so a
// repo with no binding file runs exactly as today.
func LoadAgents(dataDir string) (AgentsConfig, error) {
	path := filepath.Join(dataDir, AgentsConfigName)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return AgentsConfig{}, nil
		}
		return AgentsConfig{}, err
	}
	// Decode into a generic table so EVERY top-level section can be classified:
	// `executor`/`reviewer` are the built-in roles; any OTHER top-level table is a
	// named agent in the FLAT form [<name>] (sty_6e0ba71c). The legacy nested
	// container [agents.<name>] is still read for back-compat.
	var raw map[string]toml.Primitive
	md, err := toml.Decode(string(b), &raw)
	if err != nil {
		return AgentsConfig{}, err
	}
	ac := AgentsConfig{Agents: map[string]AgentBinding{}}
	for key, prim := range raw {
		switch key {
		case "executor":
			if err := md.PrimitiveDecode(prim, &ac.Executor); err != nil {
				return AgentsConfig{}, err
			}
		case "reviewer":
			if err := md.PrimitiveDecode(prim, &ac.Reviewer); err != nil {
				return AgentsConfig{}, err
			}
		case "agents": // legacy nested [agents.<name>] container (back-compat)
			nested := map[string]AgentBinding{}
			if err := md.PrimitiveDecode(prim, &nested); err != nil {
				return AgentsConfig{}, err
			}
			for n, bnd := range nested {
				ac.Agents[n] = bnd
			}
		default: // flat [<name>] — a named isolated agent
			var bnd AgentBinding
			if err := md.PrimitiveDecode(prim, &bnd); err != nil {
				return AgentsConfig{}, err
			}
			ac.Agents[key] = bnd
		}
	}
	return ac, nil
}
