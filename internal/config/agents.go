package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/bobmcallan/satelle/internal/agentcli"
)

// AgentsConfigName is the per-repo agents-binding file. It lives in the workflows
// dir (.satelle/workflows/agents.toml) beside the two route halves it binds:
// step.md names a performer and its gates by SECTION NAME, and this file says
// what those names actually run (sty_10f732ed). ActorsConfigName is the
// now-removed legacy filename — it is no longer loaded (sty_7db2ed7d); `satelle
// reindex` warns a repo still carrying it so the rename is enforced rather than
// silently honoured.
const (
	AgentsConfigName = "agents.toml"
	ActorsConfigName = "actors.toml"
	// AgentsConfigDir is the data-dir-relative directory holding AgentsConfigName.
	AgentsConfigDir = "workflows"
)

// AgentsRel is the data-dir-relative slash path of the repo agents layer — the
// spelling every message, server key and sync entry uses so the canonical
// location has ONE spelling.
const AgentsRel = AgentsConfigDir + "/" + AgentsConfigName

// AgentsPath resolves the repo agents layer, preferring the canonical location
// and falling back to the legacy one beside satelle.toml.
//
// The fallback is what keeps an unconverted repo alive: an initialized repo with
// no loadable agents layer REFUSES to run (requireAgents, sty_d0d6bb67), so a
// hard cutover would brick every repo that had not re-inited. `satelle init`
// relocates the file and reports it; until then the legacy path is read in place.
//
// When NEITHER exists the canonical path is returned with legacy=false, so a
// "missing agents.toml" message names where the file belongs rather than where it
// used to live.
func AgentsPath(dataDir string) (path string, legacy bool) {
	canonical := filepath.Join(dataDir, AgentsConfigDir, AgentsConfigName)
	if _, err := os.Stat(canonical); err == nil {
		return canonical, false
	}
	old := filepath.Join(dataDir, AgentsConfigName)
	if _, err := os.Stat(old); err == nil {
		return old, true
	}
	return canonical, false
}

// Default agent grants — the BOOTSTRAP values a binding's empty fields resolve
// to: the executor drives in-loop (the agent itself); the reviewer runs as an
// isolated agent with a READ-ONLY tool grant (see the satelle-agent-model
// principle — the reviewer is limited to reviewing). They fill blanks INSIDE a
// loaded agents.toml; they are not a substitute for the file itself — an
// initialized repo without a loadable agents.toml refuses to run (the CLI
// bootstrap's requireAgents, sty_d0d6bb67).
const (
	DefaultExecutorCommand = "in-loop"
	// DefaultReviewerCommand is the full claude command template (the canonical
	// seed for a [reviewer] that omits command). It carries the read-only
	// --disallowedTools denylist so the grant is a ceiling. A repo overrides it
	// with its own multi-token command template in agents.toml. Bare single-token
	// presets are no longer accepted on the agents.toml path.
	DefaultReviewerCommand = agentcli.DefaultClaudeCommand
	DefaultReviewerTools   = "Read,Grep,Glob"
)

// Role values for AgentBinding.Role — the declared identity of a binding
// (sty_fc670c9b / epic:agent-invoke-unify). Role is orthogonal to the section
// name: judge-vs-perform is derived from role, not from the literal "reviewer".
const (
	RoleReviewer = "reviewer"
	RoleAgent    = "agent"
)

// Interface values for AgentBinding.Interface — how satelle runs the isolated
// worker subprocess (epic:agent-dispatch-transport). Orthogonal to role:
// command = full argv template (default; any CLI including Claude);
// acp = Agent Client Protocol over stdio (spawn line only; satelle is client).
const (
	InterfaceCommand = "command"
	InterfaceACP     = "acp"

	// Dispatch marker environment keys identify an isolated performing step to
	// harness hooks. They let the dispatched child use its authored tool grant
	// during the transition's in-flight window without opening that window to
	// the parent driving session. They are an honest-posture boundary, not a
	// sandbox: a process that bypasses or spoofs hooks is outside this contract.
	DispatchAgentEnv = "SATELLE_DISPATCH_AGENT"
	DispatchStepEnv  = "SATELLE_DISPATCH_STEP"
	DispatchItemEnv  = "SATELLE_DISPATCH_ITEM"
)

// Principles selector tokens for AgentBinding.Principles — which principles ride
// in an isolated agent's briefing (design sty_69fd4e20 §5).
const (
	PrinciplesSession = "session"
	PrinciplesAll     = "all"
	PrinciplesSystem  = "system"
	PrinciplesProject = "project"
	PrinciplesNone    = "none"
)

// AgentBinding binds one agent to a backend (how/where it runs) and its grant
// (the tool allowance, and an optional model). Empty fields take the defaults.
//
// Role declares whether the binding is a reviewer (verdict contract) or an agent
// (performer). Principles declares which principles inject into the isolated
// briefing. InjectPrinciples is the DEPRECATED boolean alias for Principles
// (true→session, false→none); Principles wins when both are set.
//
// Interface selects the dispatch transport (epic:agent-dispatch-transport):
// "command" (default) or "acp". Shared grant fields apply to both; spawn shape differs.
type AgentBinding struct {
	// Interface is "command" | "acp". Empty means command. Unknown values fail at load.
	Interface string `toml:"interface"`
	// Command is the agent's spawn/template string.
	//   command transport: multi-token full argv template with {system}/{tools}/
	//     {model}/{settings}/{payload} (each its own argv token). Bare single-token
	//     only "in-loop"; bare claude/grok/codex rejected by agentvalidate.
	//   acp transport: ACP stdio spawn only (e.g. "grok agent stdio") — no
	//     {system}/{payload} placeholders (those ride the protocol).
	// Prefer over retired harness= (no runtime fallback; MigrateAgents rewrites).
	Command string `toml:"command"`
	// Harness is retired: still decoded for MigrateAgents; CommandTemplate ignores it.
	Harness string `toml:"harness"`
	Tools   string `toml:"tools"`
	Model   string `toml:"model"`
	// Role is "reviewer" | "agent". Empty means inferred from the section name
	// (see ResolvedRole). The binary's only hard determination for a reviewer is
	// the verdict contract; tool grant/model/command are user configuration.
	Role string `toml:"role"`
	// Principles selects which principles inject into the isolated briefing:
	// "session" (default) | "all" | "system" | "project" | "none" | comma-list.
	// Empty defaults to session. inject_principles is retired (MigrateAgents).
	Principles string `toml:"principles"`
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
	Timeout string `toml:"timeout"`
	// InjectPrinciples is retired (MigrateAgents → principles=). Not used at runtime.
	InjectPrinciples *bool `toml:"inject_principles"`
	// Settings MIRRORS claude's settings.local.json schema (env, model, permissions)
	// verbatim — no derivation, no satelle-specific shape. It is materialised into
	// the {settings} placeholder: ${VAR}-resolved (ResolveAgentEnvs, fail-fast on an
	// unknown var, same as Env), JSON-marshalled, and passed INLINE as
	// `--settings <json>`. Because --settings is CLI-tier it OVERRIDES
	// .claude/settings.local.json, so a binding that authors Settings becomes the
	// authoritative provider/auth/permissions layer for that dispatch — e.g. moving
	// [retrospective]'s GLM env under settings.env fixes its silent clobber by the
	// repo's openrouter settings.local.json (that env block wins over a bare
	// process-env overlay, but never over --settings). A binding with no Settings
	// emits no --settings flag, exactly as an empty Model drops {model}.
	Settings map[string]any `toml:"settings"`
	// Effort is optional reasoning/thinking effort for the binding (sty_657f77b9):
	// e.g. "low" | "medium" | "high". Empty means peer default. Substituted into
	// {effort} on command templates (flag dropped when empty, like {model}) and
	// applied on ACP via session/set_config_option (failure-tolerant).
	Effort string `toml:"effort"`
	// Secondary names another agents.toml binding to retry once when this
	// binding's dispatch fails with a classified rate-limit/unavailable error
	// (sty_5bf61f89). Empty inherits [defaults] secondary. Empty both = no failover.
	Secondary string `toml:"secondary"`
	// Profile names a profile in the MACHINE-WIDE catalog ($SATELLE_HOME/
	// agents.toml) this binding builds on (sty_c7dfeedf). The reference is always
	// EXPLICIT: a catalog profile that happens to share this section's name is
	// never merged in on its own, so adding a profile can never silently change a
	// repo. Repo values on this binding win over the referenced profile field by
	// field; role is identity and must not disagree. A profile may itself set
	// profile= to extend another, with cycles refused at load. See ResolveAgents.
	Profile string `toml:"profile"`
}

// AgentsDefaults is the optional [defaults] table in agents.toml (sty_5bf61f89).
type AgentsDefaults struct {
	// Secondary is the default fallback binding name for isolated agents when
	// a binding omits secondary= and the primary hits rate-limit/unavailable.
	Secondary string `toml:"secondary"`
	// UseGlobalRoles opts this repo into the machine-wide catalog's [roles]
	// defaults for bindings that name no profile= of their own (sty_c7dfeedf).
	// It is off by default and must be written by hand: without it, tier 3 of the
	// precedence ladder is skipped entirely and the catalog can only reach a
	// binding that explicitly asks for it.
	UseGlobalRoles bool `toml:"use_global_roles"`
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

// CommandTemplate resolves the effective command template for this binding.
// Only `command` is read — the deprecated `harness` field is no longer a
// runtime fallback (breaking surface: run `satelle init` to MigrateAgents).
func (b AgentBinding) CommandTemplate() string {
	return b.Command
}

// ResolvedInterface returns the effective dispatch transport: command (default)
// or acp. Unknown non-empty values are returned lowercased so LoadAgents /
// agentvalidate can reject them (epic:agent-dispatch-transport).
func (b AgentBinding) ResolvedInterface() string {
	switch strings.ToLower(strings.TrimSpace(b.Interface)) {
	case "", InterfaceCommand:
		return InterfaceCommand
	case InterfaceACP:
		return InterfaceACP
	default:
		return strings.ToLower(strings.TrimSpace(b.Interface))
	}
}

// IsACP reports whether this binding uses the ACP transport.
func (b AgentBinding) IsACP() bool {
	return b.ResolvedInterface() == InterfaceACP
}

// ResolvedRole returns the binding's effective role: the declared Role when set
// to reviewer|agent (case-insensitive), else inferred from the section name
// (section "reviewer" → reviewer, otherwise agent). section is the agents.toml
// table name ([reviewer], [planner], …).
func ResolvedRole(section string, b AgentBinding) string {
	switch strings.ToLower(strings.TrimSpace(b.Role)) {
	case RoleReviewer:
		return RoleReviewer
	case RoleAgent:
		return RoleAgent
	}
	if strings.EqualFold(strings.TrimSpace(section), RoleReviewer) {
		return RoleReviewer
	}
	return RoleAgent
}

// RoleInferred reports whether role was not declared and will be inferred from
// the section name — used by agent validate/show to warn the operator to declare it.
func RoleInferred(b AgentBinding) bool {
	r := strings.ToLower(strings.TrimSpace(b.Role))
	return r != RoleReviewer && r != RoleAgent
}

// GrantsContextChannel reports whether a binding's tool grant gives a DISPATCHED
// agent a context channel — the pull-context contract (sty_47d31300). A dispatched
// performer starts with no conversation history and reconstructs its context by
// PULLING the story, its documents, and the ledger. Two channels satisfy it
// (sty_565a0202):
//
//  1. satelle CLI via shell: `Bash(satelle…)`, broad `Bash`/`Bash(*)`, or `*`.
//  2. Disk reads of story documents under the home-keyed runtime plane
//     (~/.satelle/<repo-key>/stories/<id>/) via the grok-native `read_file`
//     tool (used when headless Grok cannot enable run_terminal_command).
//     The in-repo `.satelle/stories/` path is obsolete (sty_58fa970e).
//
// A grant with neither channel leaves the agent silently context-starved, so
// dispatch refuses it loudly. Claude-only `Read` (without Bash) is intentionally
// NOT accepted: the Claude pull path is the satelle CLI, not a disk-first rubric.
//
// REVIEWERS need no channel — satelle injects the attachments into the transition
// payload's docs array and reviewer bindings never reach dispatch. A shell grant
// on a reviewer is therefore unused capability, not a requirement.
//
// This is the SINGLE owner of the rule (sty_87c0ef37): the runtime dispatch
// refusal and `satelle agent validate` both call it, so they cannot disagree
// about any grant string. It carries its own quote-stripping tokenizer rather
// than reusing splitList so that a quoted TOML token ("Bash(satelle:*)") is
// judged identically on both paths.
func GrantsContextChannel(tools string) bool {
	for _, raw := range strings.Split(tools, ",") {
		t := strings.Trim(strings.TrimSpace(raw), `"'`)
		if t == "" {
			continue
		}
		if t == "*" || t == "Bash" || t == "Bash(*)" || strings.HasPrefix(t, "Bash(satelle") {
			return true
		}
		if t == "read_file" {
			return true
		}
	}
	return false
}

// ShellGrantToken returns the first token in a tool grant that confers shell
// access, or "" when none does. It is a REPORTING helper — it names which token
// a finding is about — and is deliberately not the channel decision, which
// GrantsContextChannel alone owns.
func ShellGrantToken(tools string) string {
	for _, raw := range strings.Split(tools, ",") {
		t := strings.Trim(strings.TrimSpace(raw), `"'`)
		if t == "" {
			continue
		}
		if t == "*" || t == "Bash" || t == "Bash(*)" || strings.HasPrefix(t, "Bash(") {
			return t
		}
	}
	return ""
}

// IsInLoopCommand reports whether a binding command is the in-loop preset (single
// token "in-loop", case-insensitive). An in-loop binding is performed by the
// driving session and never dispatched as a child, so it needs no context
// channel and cannot produce an isolated verdict. Empty command is NOT in-loop:
// tests and bootstrap leave command blank and wire a runner directly.
func IsInLoopCommand(cmd string) bool {
	fields := strings.Fields(strings.TrimSpace(cmd))
	return len(fields) == 1 && strings.EqualFold(fields[0], "in-loop")
}

// ResolvedPrinciples returns the principles selector: Principles when set,
// else session. The deprecated inject_principles field is no longer a runtime
// fallback (breaking surface: run `satelle init` to MigrateAgents).
func (b AgentBinding) ResolvedPrinciples() string {
	if p := strings.TrimSpace(b.Principles); p != "" {
		return normalizePrinciplesSelector(p)
	}
	return PrinciplesSession
}

// InjectsPrinciples reports whether this binding injects any principles (and the
// constitution order-zero block) into the isolated agent's context — true unless
// the resolved selector is none.
func (b AgentBinding) InjectsPrinciples() bool {
	return b.ResolvedPrinciples() != PrinciplesNone
}

// normalizePrinciplesSelector lowercases, trims, and re-joins comma-list tokens.
func normalizePrinciplesSelector(s string) string {
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return PrinciplesSession
	}
	if len(out) == 1 {
		return out[0]
	}
	return strings.Join(out, ",")
}

// AgentsConfig is the on-disk shape at .satelle/workflows/agents.toml — the agents layer.
// Every field is optional; the *Binding resolvers supply today's defaults, so
// the zero value (and an absent file) is the current behaviour. Agents holds
// OPTIONAL named agents (beyond the executor/reviewer roles) declared as flat
// top-level [<name>] sections — consistent with [executor]/[reviewer] — or the
// legacy nested [agents.<name>] (still read for back-compat). A workflow node may
// allocate a step to one, and a named agent is ALWAYS isolated (see
// satelle-agent-model). LoadAgents does the classification; the toml tag here is
// retained only for the legacy nested form.
type AgentsConfig struct {
	Defaults AgentsDefaults          `toml:"defaults"`
	Executor AgentBinding            `toml:"executor"`
	Reviewer AgentBinding            `toml:"reviewer"`
	Agents   map[string]AgentBinding `toml:"agents"`
}

// ResolveSecondary returns the fallback binding for section/b when secondary is
// configured (per-binding wins over [defaults] secondary). ok is false when
// unconfigured or the named binding is missing (sty_5bf61f89).
func (a AgentsConfig) ResolveSecondary(section string, b AgentBinding) (sec AgentBinding, name string, ok bool) {
	name = strings.TrimSpace(b.Secondary)
	if name == "" {
		name = strings.TrimSpace(a.Defaults.Secondary)
	}
	if name == "" {
		return AgentBinding{}, "", false
	}
	if strings.EqualFold(name, strings.TrimSpace(section)) {
		return AgentBinding{}, "", false // refuse self-loop
	}
	switch strings.ToLower(name) {
	case "reviewer":
		rb := a.Reviewer
		if rb.CommandTemplate() == "" {
			rb.Command = DefaultReviewerCommand
		}
		return rb, "reviewer", true
	case "executor":
		return a.Executor, "executor", true
	}
	nb, found := a.NamedBinding(name)
	if !found {
		return AgentBinding{}, name, false
	}
	return nb, name, true
}

// NamedBinding resolves an optional named agent declared as a flat top-level
// [<name>] section (or the legacy nested [agents.<name>]). ok is false when none is
// declared, so a workflow node that allocates a step to an absent agent degrades
// gracefully to the in-loop executor. A named agent is always isolated; an unset
// command defaults to the isolated claude preset.
func (a AgentsConfig) NamedBinding(name string) (AgentBinding, bool) {
	b, ok := a.Agents[name]
	if !ok {
		return AgentBinding{}, false
	}
	b.Command = b.CommandTemplate()
	if b.Command == "" {
		b.Command = DefaultReviewerCommand
	}
	return b, true
}

// ReviewerBinding resolves the reviewer agent's backend and grant, defaulting to
// an isolated agent with the read-only tool grant. The grant travels with the
// binding, so the reviewer's read-only limit holds whatever the backend.
func (a AgentsConfig) ReviewerBinding() AgentBinding {
	b := a.Reviewer
	b.Command = b.CommandTemplate()
	if b.Command == "" {
		b.Command = DefaultReviewerCommand
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
	b.Command = b.CommandTemplate()
	if b.Command == "" {
		b.Command = DefaultExecutorCommand
	}
	return b
}

// LoadAgents reads the agents layer through AgentsPath — the canonical
// <dataDir>/workflows/agents.toml, or the legacy <dataDir>/agents.toml while a
// repo is unconverted. The legacy actors.toml is no longer read (sty_7db2ed7d);
// an absent agents.toml yields the zero AgentsConfig — defaults via the *Binding
// resolvers — and a nil error. Absence is judged by the CALLER: the CLI bootstrap
// treats a missing file in an initialized repo as broken and refuses to run
// (requireAgents, sty_d0d6bb67); pre-init surfaces (nothing to load yet) keep the
// zero-config bootstrap.
func LoadAgents(dataDir string) (AgentsConfig, error) {
	path, _ := AgentsPath(dataDir)
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
		case "defaults":
			if err := md.PrimitiveDecode(prim, &ac.Defaults); err != nil {
				return AgentsConfig{}, err
			}
		case "executor":
			if err := md.PrimitiveDecode(prim, &ac.Executor); err != nil {
				return AgentsConfig{}, err
			}
		case "reviewer":
			if err := md.PrimitiveDecode(prim, &ac.Reviewer); err != nil {
				return AgentsConfig{}, err
			}
		case "agents":
			// Nested [agents.<name>] is no longer a live dual-read. Ignore the
			// table so an un-migrated file does not silently load nested agents;
			// MigrateAgents flattens it on init.
			continue
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
	if err := ac.validateInterfaces(); err != nil {
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
		return checkBindingTimeout(AgentsConfigName, section, b)
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

// validateInterfaces fails fast on an unknown interface= value
// (epic:agent-dispatch-transport). Empty is fine (defaults to command).
func (a AgentsConfig) validateInterfaces() error {
	check := func(section string, b AgentBinding) error {
		return checkBindingInterface(AgentsConfigName, section, b)
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
