package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// AgentsConfigName is the per-repo agents-binding file. It lives in the workflows
// dir (.satelle/workflows/agents.toml) beside the two route halves it binds:
// step.toml names a performer and its gates by SECTION NAME, and this file says
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

// WorkspaceAgentsName is the SYNCED workspace bindings layer (sty_01949949):
// what `satelle sync bindings pull` materialises from the bound team
// workspace's publish catalog. It is a separate file beside the authored
// agents.toml — sync never rewrites authored bytes — and it is a LAYER UNDER
// the repo's own file (see resolveagents.go: a repo table wins field by field;
// a workspace-only table applies). Like agents.toml it is machine configuration
// the directory monitor must not index as a workflow doc.
const WorkspaceAgentsName = "agents.workspace.toml"

// WorkspaceAgentsRel is the data-dir-relative slash path of the workspace layer.
const WorkspaceAgentsRel = AgentsConfigDir + "/" + WorkspaceAgentsName

// WorkspaceAgentsPath is where the synced workspace bindings layer lives.
func WorkspaceAgentsPath(dataDir string) string {
	return filepath.Join(dataDir, AgentsConfigDir, WorkspaceAgentsName)
}

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
// acp = Agent Client Protocol over stdio (spawn line only; satelle is client);
// stream = Claude stream-json live session (sty_d244fe1b).
const (
	InterfaceCommand = "command"
	InterfaceACP     = "acp"
	InterfaceStream  = "stream"

	// Dispatch marker environment keys identify an isolated performing step to
	// harness hooks. They let the dispatched child use its authored tool grant
	// during the transition's in-flight window without opening that window to
	// the parent driving session. They are an honest-posture boundary, not a
	// sandbox: a process that bypasses or spoofs hooks is outside this contract.
	DispatchAgentEnv = "SATELLE_DISPATCH_AGENT"
	DispatchStepEnv  = "SATELLE_DISPATCH_STEP"
	DispatchItemEnv  = "SATELLE_DISPATCH_ITEM"

	// Relay marker environment keys identify a rework-relay coder spawn to
	// harness hooks (sty_7567f047). Distinct from SATELLE_DISPATCH_*: the
	// dispatch branch requires InFlight, and the relay runs at a committed
	// status. Same honest-posture boundary as the dispatch markers.
	RelayBindingEnv = "SATELLE_RELAY_BINDING"
	RelayItemEnv    = "SATELLE_RELAY_ITEM"
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
// "command" (default), "acp", or "stream". Shared grant fields apply to all;
// spawn shape differs.
type AgentBinding struct {
	// Interface is "command" | "acp" | "stream". Empty means command.
	// Unknown values fail at load.
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
	// LiveInterfaceOrder is the preference order ResolveInterface walks for a
	// binding with no interface= that is used LIVE (epic:model-selection child
	// 2) — the constitution's "no opinion as code": the binary only detects
	// which transports a binding's CLI can serve, and this configuration says
	// which capable one it prefers. Empty means the shipped default order
	// (LiveInterfaces()).
	LiveInterfaceOrder []string `toml:"live_interfaces"`
}

// LiveInterfaces returns the preference order ResolveInterface walks for a
// live use: the configured live_interfaces when set, else the shipped default
// [stream, acp].
func (d AgentsDefaults) LiveInterfaces() []string {
	if len(d.LiveInterfaceOrder) > 0 {
		return d.LiveInterfaceOrder
	}
	return []string{InterfaceStream, InterfaceACP}
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

// ResolvedInterface returns the effective dispatch transport: command (default),
// acp, or stream. Unknown non-empty values are returned lowercased so
// LoadAgents / agentvalidate can reject them (epic:agent-dispatch-transport).
func (b AgentBinding) ResolvedInterface() string {
	switch strings.ToLower(strings.TrimSpace(b.Interface)) {
	case "", InterfaceCommand:
		return InterfaceCommand
	case InterfaceACP:
		return InterfaceACP
	case InterfaceStream:
		return InterfaceStream
	default:
		return strings.ToLower(strings.TrimSpace(b.Interface))
	}
}

// InterfaceUse says how a binding will be invoked, so ResolveInterface can pick
// the best transport for an unset interface= (epic:model-selection child 2):
// a one-shot dispatch (gate reviewer, planner, edge advisor) always resolves to
// command; a live session (rework relay seat, rework.consult, story chat /
// orchestrator) resolves to the CLI's best live transport.
type InterfaceUse int

const (
	// UseOneShot is a single request/response dispatch — a gate verdict, a
	// planner run, a step summary. Always resolves to command.
	UseOneShot InterfaceUse = iota
	// UseLive is a multi-turn session the caller keeps open and talks to.
	UseLive
)

// liveCapableInterface reports whether iface is a live transport the binding's
// AUTHORED command can actually open. Two independent questions, both must
// hold:
//
//   - MECHANISM: which CLI is this? Only Claude speaks the stream-json
//     protocol, so stream is restricted to a "claude" ExecutableToken; any
//     other token is an ACP-capable spawn line and is restricted to acp. This
//     is detection, not opinion — the binary observes what the CLI IS.
//   - SHAPE: can the authored argv actually serve that transport? This is
//     answered by running the SAME construction the runtime opens with
//     (agentcli.RunnerFromBinding), not guessed. A "claude" token is
//     necessary but not sufficient: an authored ONE-SHOT command (e.g.
//     DefaultReviewerCommand's `claude -p --output-format json …
//     --append-system-prompt {system} …`) carries {system}, which
//     newStreamRunner rejects (system/payload ride the live protocol, not
//     argv) — so it correctly reads as not stream-capable rather than being
//     waved through on "first token is claude" and failing (or opening
//     broken) at session-open time.
//
// An empty command has no shape yet, so the transport still needs to supply
// its OWN default command line (DefaultCommandFor) for the binding to ever
// open — acp has none (satelle cannot guess an ACP spawn line), so an empty
// command is a candidate only for a transport DefaultCommandFor actually
// fills in. Without this, live_interfaces = ["acp", "stream"] would resolve
// an empty-command binding to acp with an empty spawn line — a "resolution"
// that can never open, exactly the kind of broken result this function
// exists to rule out.
func liveCapableInterface(command, iface string) bool {
	if strings.TrimSpace(command) == "" {
		return DefaultCommandFor(iface) != ""
	}
	token := ExecutableToken(command)
	switch iface {
	case InterfaceStream:
		if token != "claude" {
			return false
		}
	case InterfaceACP:
		if token == "claude" {
			return false
		}
	default:
		return false
	}
	_, err := agentcli.RunnerFromBinding(iface, command)
	return err == nil
}

// ResolveInterface resolves a binding's effective transport for use, and states
// why:
//
//   - "explicit" — Interface is set; never overridden, whatever use is.
//   - "one-shot default" — a genuine UseOneShot dispatch (gate, planner, edge
//     advisor): always resolves to command.
//   - "live use: in-loop" — UseLive, but the command is the in-loop preset,
//     which has no live transport at all (command is inert there, never a
//     fabricated live one). Distinct from "one-shot default": this IS a live
//     use, it just cannot be one, and the reason says so rather than
//     borrowing the one-shot label.
//   - "live use" — UseLive, resolved to the first live-capable transport in
//     a.Defaults.LiveInterfaces() that can actually open the binding's RAW,
//     undefaulted command (liveCapableInterface reuses the real runner
//     construction and requires a usable default command line for an
//     unauthored command, so "no command yet" only credits a transport that
//     can really open, and an authored one-shot command that cannot serve a
//     live transport is excluded rather than guessed capable from its first
//     token).
//   - "live use: not live-capable" — UseLive, but no configured live
//     transport could serve the command (or, for an empty command, none of
//     them has a usable default line) — falls back to command, and the
//     existing not-live-capable WARN/refusal fires with this reason named
//     explicitly rather than the resolution reading as an ordinary success.
func (a AgentsConfig) ResolveInterface(b AgentBinding, use InterfaceUse) (iface, reason string) {
	if strings.TrimSpace(b.Interface) != "" {
		return b.ResolvedInterface(), "explicit"
	}
	if use == UseOneShot {
		return b.ResolvedInterface(), "one-shot default"
	}
	if IsInLoopCommand(b.Command) {
		return b.ResolvedInterface(), "live use: in-loop"
	}
	for _, cand := range a.Defaults.LiveInterfaces() {
		if liveCapableInterface(b.Command, cand) {
			return cand, "live use"
		}
	}
	return InterfaceCommand, "live use: not live-capable"
}

// DefaultCommandFor returns the command template a resolved interface supplies
// when a binding has no command of its own — "" for acp, which needs an
// authored spawn line (satelle cannot guess one).
func DefaultCommandFor(iface string) string {
	switch iface {
	case InterfaceStream:
		return agentcli.DefaultClaudeStreamCommand
	case InterfaceCommand:
		return DefaultReviewerCommand
	default:
		return ""
	}
}

// EffectiveBinding returns a copy of b with Interface resolved for use
// (ResolveInterface) and, when b.Command is empty, Command filled from the
// resolved interface's default. This is the SINGLE seam live callers
// (agentstep.Engine.OpenSessionAs) and `satelle agent validate` share, so
// neither can resolve or report a transport the other would not open
// (sty_8e0b29a0's validate/runtime invariant, extended to the derived default).
func (a AgentsConfig) EffectiveBinding(b AgentBinding, use InterfaceUse) AgentBinding {
	iface, _ := a.ResolveInterface(b, use)
	eb := b
	eb.Interface = iface
	if strings.TrimSpace(eb.Command) == "" {
		eb.Command = DefaultCommandFor(iface)
	}
	return eb
}

// RawBinding returns the binding named name exactly as authored — the
// executor/reviewer sections or an [<name>] entry — with NO command
// defaulting, so EffectiveBinding can tell "no command yet" (every live
// transport a candidate) from "already claude-shaped". Unlike NamedBinding,
// this is not a dispatch resolver: it is the raw material ResolveInterface,
// EffectiveBinding and LiveBinding resolve from — exported so a caller that
// needs the resolved reason alongside the effective binding (e.g. `satelle
// agent validate`) can call ResolveInterface itself on the same raw value.
func (a AgentsConfig) RawBinding(name string) (AgentBinding, bool) {
	switch name {
	case "executor":
		return a.Executor, true
	case "reviewer":
		return a.Reviewer, true
	}
	b, ok := a.Agents[name]
	return b, ok
}

// LiveRawBinding is RawBinding plus each role's OWN baseline default — the
// same one its one-shot resolver (ExecutorBinding / ReviewerBinding) would
// apply — so a caller that needs BOTH the resolved reason (ResolveInterface)
// and the effective binding (EffectiveBinding) for a live use of "executor" or
// "reviewer" computes them from the identical starting point LiveBinding uses,
// rather than re-deriving from the bare RawBinding and disagreeing with it
// (sty_119f6fda: `satelle agent validate`'s grant loop did exactly that).
//
// "executor" and "reviewer" are reachable as a live-use name (rework.consult=
// executor or =reviewer is a legitimate config, checked by agentvalidate's
// rework-alloc rule) but RawBinding deliberately returns them with NO role
// defaulting, so this layers each role's default in first, before
// ResolveInterface/EffectiveBinding ever see Interface/Command:
//
//   - reviewer: an empty Tools grant becomes DefaultReviewerTools (read-only),
//     matching the ceiling ReviewerBinding() gives the one-shot gate path — a
//     consulted reviewer must not silently lose that ceiling just because it
//     is opened live instead of dispatched as a gate.
//   - executor: an empty Command becomes DefaultExecutorCommand ("in-loop").
//     Unlike every other binding, an unset executor command has an ESTABLISHED
//     meaning already (ExecutorBinding: "the driving agent itself") — it is
//     not "no command yet, pick me a live default". Defaulting it here BEFORE
//     ResolveInterface/EffectiveBinding lets IsInLoopCommand see "in-loop" and
//     resolve/report it as such, instead of the empty command reading as
//     "every live transport is a candidate" and reporting (or opening) a real
//     Claude subprocess for what is configured to be the driving session
//     itself.
func (a AgentsConfig) LiveRawBinding(name string) (AgentBinding, bool) {
	b, ok := a.RawBinding(name)
	if !ok {
		return AgentBinding{}, false
	}
	switch name {
	case "reviewer":
		if b.Tools == "" {
			b.Tools = DefaultReviewerTools
		}
	case "executor":
		if b.Command == "" {
			b.Command = DefaultExecutorCommand
		}
	}
	return b, true
}

// LiveBinding resolves name's binding as EffectiveBinding(UseLive) over
// LiveRawBinding(name) — the transport and command a live session
// (agentstep.Engine.OpenSessionAs, and so `satelle story chat` and the rework
// relay) actually opens, and what `satelle agent validate` reports for a live
// use of that binding.
func (a AgentsConfig) LiveBinding(name string) (AgentBinding, bool) {
	b, ok := a.LiveRawBinding(name)
	if !ok {
		return AgentBinding{}, false
	}
	return a.EffectiveBinding(b, UseLive), true
}

// IsACP reports whether this binding uses the ACP transport.
func (b AgentBinding) IsACP() bool {
	return b.ResolvedInterface() == InterfaceACP
}

// IsStream reports whether this binding uses the stream-json transport.
func (b AgentBinding) IsStream() bool {
	return b.ResolvedInterface() == InterfaceStream
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
	return loadAgentsBody(string(b))
}

// loadAgentsBody classifies an agents-layer body through the ONE decoder
// (decodeAgents, live form: nested [agents.<name>] ignored — MigrateAgents
// flattens it on init) and applies the fail-fast load checks.
func loadAgentsBody(body string) (AgentsConfig, error) {
	ac, err := decodeAgents(body, false)
	if err != nil {
		return AgentsConfig{}, err
	}
	if err := ac.validateTimeouts(); err != nil {
		return AgentsConfig{}, err
	}
	if err := ac.validateInterfaces(); err != nil {
		return AgentsConfig{}, err
	}
	return ac, nil
}

// LoadWorkspaceAgents reads the synced workspace bindings layer
// (WorkspaceAgentsPath). An absent file is the zero config and a nil error, so
// a repo that never pulled one resolves byte-identically to today; a present
// but malformed file is an error, so a broken layer fails loud rather than
// silently dropping out of the ladder (sty_01949949).
func LoadWorkspaceAgents(dataDir string) (AgentsConfig, error) {
	b, err := os.ReadFile(WorkspaceAgentsPath(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return AgentsConfig{}, nil
		}
		return AgentsConfig{}, err
	}
	ac, err := loadAgentsBody(string(b))
	if err != nil {
		return AgentsConfig{}, fmt.Errorf("%s: %w", WorkspaceAgentsRel, err)
	}
	return ac, nil
}

// ExecutableToken returns the program a command template would spawn — its
// first whitespace-separated token — or "" for the in-loop / empty command
// (nothing is spawned). It is what local resolution looks up on PATH
// (sty_01949949 AC3): a workspace-published binding names `claude`, and THIS
// machine decides whether `claude` exists.
func ExecutableToken(command string) string {
	if IsInLoopCommand(command) {
		return ""
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// ExecutableToken is ExecutableToken over the binding's effective command template.
func (b AgentBinding) ExecutableToken() string {
	return ExecutableToken(b.CommandTemplate())
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
// (epic:agent-dispatch-transport), or an unknown token in [defaults]
// live_interfaces (epic:model-selection child 2). Empty is fine (defaults to
// command / the shipped live order).
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
	for _, raw := range a.Defaults.LiveInterfaceOrder {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case InterfaceStream, InterfaceACP:
		default:
			return fmt.Errorf("%s [defaults] live_interfaces %q: want %q or %q",
				AgentsConfigName, raw, InterfaceStream, InterfaceACP)
		}
	}
	return nil
}
