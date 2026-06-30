package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// AgentsConfigName is the per-repo agents-binding file, beside satelle.toml under
// the data dir (.satelle/agents.toml). ActorsConfigName is the now-removed legacy
// filename — it is no longer loaded (sty_7db2ed7d); `satelle validate` flags a repo
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
type AgentBinding struct {
	Harness string `toml:"harness"`
	Tools   string `toml:"tools"`
	Model   string `toml:"model"`
}

// AgentsConfig is the on-disk shape at .satelle/agents.toml — the agents layer.
// Every field is optional; the *Binding resolvers supply today's defaults, so
// the zero value (and an absent file) is the current behaviour. Agents holds
// OPTIONAL named agents (beyond the executor/reviewer roles) declared under
// [agents.<name>] — a workflow node may allocate a step to one, and a named agent
// is ALWAYS isolated (see satelle-agent-model).
type AgentsConfig struct {
	Executor AgentBinding            `toml:"executor"`
	Reviewer AgentBinding            `toml:"reviewer"`
	Agents   map[string]AgentBinding `toml:"agents"`
}

// NamedBinding resolves an optional named agent declared under [agents.<name>].
// ok is false when none is declared, so a workflow node that allocates a step to an
// absent agent degrades gracefully to the in-loop executor. A named agent is always
// isolated; an unset harness defaults to the isolated claude preset.
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
	var ac AgentsConfig
	if _, err := toml.Decode(string(b), &ac); err != nil {
		return AgentsConfig{}, err
	}
	return ac, nil
}
