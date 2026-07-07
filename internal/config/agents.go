package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

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

// Default agent grants — the BOOTSTRAP values a binding's empty fields resolve
// to: the executor drives in-loop (the agent itself); the reviewer runs as an
// isolated agent with a READ-ONLY tool grant (see the satelle-agent-model
// principle — the reviewer is limited to reviewing). They fill blanks INSIDE a
// loaded agents.toml; they are not a substitute for the file itself — an
// initialized repo without a loadable agents.toml refuses to run (the CLI
// bootstrap's requireAgents, sty_d0d6bb67).
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
	Harness string `toml:"harness"`
	Tools   string `toml:"tools"`
	Model   string `toml:"model"`
	// Env sets environment variables on the dispatched agent's process (layered
	// onto os.Environ, binding keys winning). Each value may reference the [vars]
	// KV via ${NAME}, resolved at CLI wiring time (ResolveAgentEnvs) — how a step
	// points at an alternate model backend, e.g. env = { ANTHROPIC_BASE_URL =
	// "https://api.z.ai/api/anthropic", ANTHROPIC_AUTH_TOKEN = "${GLM_API_KEY}" }.
	// The in-loop executor never execs a child, so its Env is inert (sty_001558ce).
	Env map[string]string `toml:"env"`
	// Timeout bounds ONE dispatch of this binding — a Go duration string (e.g.
	// "45m"). Empty inherits the engine's default dispatch bound. A from-scratch
	// code-writing worker needs a longer window than the 20m default (a real
	// feature was SIGKILLed at exactly 20m — sty_b73c3236), so the bound is authored
	// config, not a compiled constant (sty_446c38b7). Applies to a DISPATCHED named
	// executor; reviewer/summariser gate invocations keep the engine's agent bound.
	Timeout          string `toml:"timeout"`
	InjectPrinciples *bool  `toml:"inject_principles"`
}

// TimeoutDuration resolves this binding's dispatch bound: the parsed Timeout when
// set, else def. A malformed or non-positive Timeout is an error — LoadAgents
// validates it at load (validateTimeouts) so a dispatch never silently falls back
// on a typo (sty_446c38b7).
func (b AgentBinding) TimeoutDuration(def time.Duration) (time.Duration, error) {
	if b.Timeout == "" {
		return def, nil
	}
	d, err := time.ParseDuration(b.Timeout)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("timeout %q must be positive", b.Timeout)
	}
	return d, nil
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
// zero AgentsConfig — defaults via the *Binding resolvers — and a nil error.
// Absence is judged by the CALLER: the CLI bootstrap treats a missing file in an
// initialized repo as broken and refuses to run (requireAgents, sty_d0d6bb67);
// pre-init surfaces (nothing to load yet) keep the zero-config bootstrap.
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
	if err := ac.validateTimeouts(); err != nil {
		return AgentsConfig{}, err
	}
	return ac, nil
}

// validateTimeouts fails fast on a malformed or non-positive [<section>] timeout
// so a typo is caught at load — consistent with the rest of agents.toml's
// fail-fast bootstrap — rather than surfacing only when that binding dispatches
// (sty_446c38b7).
func (a AgentsConfig) validateTimeouts() error {
	check := func(section string, b AgentBinding) error {
		if _, err := b.TimeoutDuration(0); err != nil {
			return fmt.Errorf("agents.toml [%s] timeout: %w", section, err)
		}
		return nil
	}
	if err := check("executor", a.Executor); err != nil {
		return err
	}
	if err := check("reviewer", a.Reviewer); err != nil {
		return err
	}
	for name, b := range a.Agents {
		if err := check(name, b); err != nil {
			return err
		}
	}
	return nil
}
