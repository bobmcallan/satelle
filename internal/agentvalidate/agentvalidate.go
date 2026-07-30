// Package agentvalidate is the store-free, deterministic check of the agents
// layer (.satelle/agents.toml) and each workflow's agent= node bindings.
//
// It is the SINGLE authority three callers share (sty_93eec36d):
//   - `satelle agent validate` (standalone, on-demand)
//   - `satelle init` deployment validation
//   - story engagement (leaving the workflow entry state for a non-cancel target)
//
// It deliberately does NOT re-implement structure.Doc (performing-node rubrics)
// or agentstep.WorkflowConsistency (unresolved gate skills / ambiguous
// applies_to) — those stay owned by their existing checks. This package adds
// only: every binding's command/timeout/env resolves, each agent's resolved
// grant is inspectable, every agent=<name> node has a matching binding, and
// orphaned named bindings are flagged.
package agentvalidate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfhook"
)

// Grant is one agent's resolved, inspectable capability surface — what validate
// surfaces so a preset's baked grant is visible without knowing the expansion.
// Env VALUES are never included (secrets); key names may appear in Notes.
type Grant struct {
	Name      string
	Backend   string // in-loop | isolated:claude | isolated:grok | isolated:codex | isolated:<binary> | acp:<binary>
	Interface string // command | acp (epic:agent-dispatch-transport)
	// Command is the effective command template — the literal argv the operator
	// can read. Surfaced as a field (not only inside Notes) so a provenance
	// display can attribute it like any other resolved value (sty_c7dfeedf).
	Command string
	// Secondary is the rate-limit failover binding name (sty_5bf61f89), empty
	// when unconfigured.
	Secondary         string
	Tools             string
	Model             string
	Effort            string // optional reasoning effort (sty_657f77b9)
	Timeout           string
	ReadOnly          bool
	InjectsPrinciples bool
	Role              string // resolved role: reviewer | agent (sty_e21cbc08)
	Principles        string // resolved principles selector
	RoleInferred      bool   // true when role was not declared in agents.toml
	Notes             string // non-secret notes (e.g. env key names, command ceiling hints)
	// ContextChannel reports whether the grant carries a pull-context channel —
	// what a DISPATCHED performer needs to reconstruct its context, and what a
	// reviewer never uses (sty_87c0ef37). Inspectable so the fact is visible
	// without re-deriving it from Tools.
	ContextChannel bool
	// Sources maps each effective field (command, tools, model, …) to the tier
	// that supplied it: "repo", "profile:<name>", "global-role:<name>", or
	// "embedded" (sty_c7dfeedf). Nil when the caller validated without resolving
	// the machine-wide catalog. An operator reading a grant can then see not only
	// WHAT the reviewer will run but WHERE that value was authored.
	Sources map[string]string
}

// GateAllocation is one workflow gate/node's resolved binding (sty_a476a2f8).
// EffectiveModel is the binding's model — agents.toml owns model, not the DOT.
type GateAllocation struct {
	Workflow       string
	Node           string // state name, "edge:from→to" for edge gates, or "hook:<operation>"
	Skill          string
	Agent          string // binding section that will run the gate
	BindingModel   string
	EffectiveModel string // same as BindingModel (no DOT override)
	// Operation is the lifecycle operation for a HOOK allocation (sty_ede16f51),
	// empty for a DOT node or edge. Hooks fire outside the status graph, so they
	// are surfaced alongside gates rather than being invisible.
	Operation string
	// Source records how a hook allocation was declared — wfhook.SourceHooks or
	// wfhook.SourceShorthand — so a display can say whether the agent was chosen
	// or defaulted. Empty for a DOT node or edge.
	Source string
}

// Report is the structured result of Validate.
// Problems are hard failures (non-zero exit / engage refuse).
// Warnings are advisory (e.g. orphaned named bindings that may still be used by
// non-workflow verbs like `story retrospect`) — printed, not failed.
type Report struct {
	Problems []string
	Warnings []string
	Grants   []Grant
	// Gates lists every gate edge / scoped reviewer node / named performer with
	// its effective model so drift audits see per-node allocation (sty_19456622).
	Gates []GateAllocation
	// Provenance is the per-binding, per-field source table when the report was
	// produced by ValidateEffective; nil for the catalog-free Validate.
	Provenance config.Provenance
}

// OK reports whether the report carries no hard problems.
func (r Report) OK() bool { return len(r.Problems) == 0 }

// ValidateEffective resolves a repo's agents layer against the machine-wide
// profile catalog and validates the RESULT — the bindings that will actually run
// (sty_c7dfeedf). A resolution failure (missing profile, reference cycle, repo/
// profile role conflict) becomes a Problem rather than an early return, so
// `satelle agent validate` reports the whole picture in one pass instead of
// stopping at the first broken reference; the un-resolved repo layer is then
// validated so the remaining findings still surface.
//
// Every other check — reviewer read-only ceiling, interface, context channel,
// workflow allocation — runs against the merged binding, so a profile cannot
// smuggle a capability past a check by supplying it from the catalog.
func ValidateEffective(repo config.AgentsConfig, global config.GlobalAgentsConfig, repoVars map[string]string, workflows []docindex.Doc) Report {
	eff, err := config.ResolveEffectiveAgents(repo, global, repoVars)
	if err != nil {
		r := validate(repo, config.LayerVars(global.Vars, repoVars), workflows, nil)
		r.Problems = append([]string{err.Error()}, r.Problems...)
		return r
	}
	return validate(eff.Agents, eff.Vars, workflows, eff.Provenance)
}

// Validate checks every agents.toml binding and each workflow's agent= node
// allocations. vars is the [vars] KV used to resolve ${VAR} in binding env/
// settings (may be nil). workflows may be empty (agent-only check). Callers that
// have already resolved the machine-wide catalog pass the effective layer here;
// ValidateEffective is the form that resolves it and reports provenance.
func Validate(agents config.AgentsConfig, vars map[string]string, workflows []docindex.Doc) Report {
	return validate(agents, vars, workflows, nil)
}

func validate(agents config.AgentsConfig, vars map[string]string, workflows []docindex.Doc, prov config.Provenance) Report {
	var r Report
	r.Provenance = prov

	// Env/settings resolution once — fail-fast naming section+key, never values.
	if _, err := config.ResolveAgentEnvs(agents, vars); err != nil {
		r.Problems = append(r.Problems, err.Error())
	}

	// Built-in roles first, then named agents in sorted order.
	type named struct {
		name string
		b    config.AgentBinding
	}
	sections := []named{
		{"executor", agents.ExecutorBinding()},
		{"reviewer", agents.ReviewerBinding()},
	}
	for _, name := range sortedNames(agents.Agents) {
		b, _ := agents.NamedBinding(name)
		sections = append(sections, named{name, b})
	}

	for _, sec := range sections {
		g, problems, warnings := checkBinding(sec.name, sec.b)
		g.Sources = prov[sec.name]
		r.Grants = append(r.Grants, g)
		r.Problems = append(r.Problems, problems...)
		r.Warnings = append(r.Warnings, warnings...)
	}

	// Workflow node → binding + orphan named bindings + per-gate effective model.
	usedNamed := map[string]bool{}
	revModel := agents.ReviewerBinding().Model
	for _, doc := range workflows {
		// Lifecycle hooks FIRST: they are frontmatter, so they must be checked even
		// when the DOT below does not parse (sty_ede16f51).
		hookProblems := checkHooks(doc, agents, revModel, usedNamed, &r)
		r.Problems = append(r.Problems, hookProblems...)

		spec, ok := wfdot.Parse(doc.Body)
		if !ok {
			continue // structure.Doc / workflow validate owns unparseable bodies
		}
		for _, st := range spec.States {
			// Step-summary opt-in (sty_9a139c78): edge-less judge/summariser, not a
			// spine performer. Named role=reviewer bindings (cheap Grok summariser)
			// are allowed — agent=<named> must not trip the performer check
			// (sty_8ee40f94).
			if st.IsSummariser() {
				sec := st.Agent
				if sec == "" {
					sec = "reviewer"
				}
				bm := revModel
				if sec != "reviewer" {
					usedNamed[sec] = true
					if b, ok := agents.NamedBinding(sec); ok {
						bm = b.Model
						if config.ResolvedRole(sec, b) != config.RoleReviewer {
							r.Problems = append(r.Problems, fmt.Sprintf(
								"workflow %q node %q allocates agent=%s with role=%q on a step-summary node (want role=reviewer)",
								doc.Name, st.Name, sec, config.ResolvedRole(sec, b)))
						}
					} else {
						r.Problems = append(r.Problems, fmt.Sprintf(
							"workflow %q node %q allocates agent=%s with no [%s] binding in agents.toml",
							doc.Name, st.Name, sec, sec))
					}
				}
				r.Gates = append(r.Gates, gateAlloc(doc.Name, st.Name, st.Skill, sec, bm))
			} else if st.Skill != "" && len(st.On) > 0 {
				// Plan D2 / sty_a476a2f8: on= marks a scoped GATE (judge); spine
				// (no on=) with agent=<name> is a PERFORMER. Split before role checks.
				// Scoped gate path — skip known performer augmentation tokens.
				switch st.Agent {
				case "executor", "planner", "coder":
					// not a gate
				default:
					sec := st.Agent
					if sec == "" {
						sec = "reviewer"
					}
					bm := revModel
					if sec != "reviewer" {
						usedNamed[sec] = true
						if b, ok := agents.NamedBinding(sec); ok {
							bm = b.Model
							if config.ResolvedRole(sec, b) != config.RoleReviewer {
								r.Problems = append(r.Problems, fmt.Sprintf(
									"workflow %q node %q allocates agent=%s with role=%q on a gated node (want role=reviewer)",
									doc.Name, st.Name, sec, config.ResolvedRole(sec, b)))
							}
						} else {
							r.Problems = append(r.Problems, fmt.Sprintf(
								"workflow %q node %q allocates agent=%s with no [%s] binding in agents.toml",
								doc.Name, st.Name, sec, sec))
						}
					}
					r.Gates = append(r.Gates, gateAlloc(doc.Name, st.Name, st.Skill, sec, bm))
				}
			} else if st.Agent != "" && st.Agent != "executor" && st.Agent != "reviewer" && len(st.On) == 0 {
				// Named PERFORMER on a spine node (no on=).
				usedNamed[st.Agent] = true
				b, ok := agents.NamedBinding(st.Agent)
				if !ok {
					r.Problems = append(r.Problems, fmt.Sprintf(
						"workflow %q node %q allocates agent=%s with no [%s] binding in agents.toml",
						doc.Name, st.Name, st.Agent, st.Agent))
				} else {
					if config.ResolvedRole(st.Agent, b) == config.RoleReviewer {
						r.Problems = append(r.Problems, fmt.Sprintf(
							"workflow %q node %q allocates agent=%s with role=reviewer on a performing node — use a gated edge or scoped on= node for judges",
							doc.Name, st.Name, st.Agent))
					}
					if p := performerChannelProblem(doc.Name, st.Name, st.Agent, b); p != "" {
						r.Problems = append(r.Problems, p)
					}
					r.Gates = append(r.Gates, gateAlloc(doc.Name, st.Name, st.Skill, st.Agent, b.Model))
				}
			}
			// on_enter_agent=<name> one-shot entry performer (sty_5cabe26f) —
			// orthogonal to agent=; must also resolve to a binding and counts
			// as a use so the binding is not flagged orphaned. It is DISPATCHED,
			// so it carries the same context-channel requirement (sty_87c0ef37).
			if st.OnEnterAgent != "" {
				usedNamed[st.OnEnterAgent] = true
				if b, ok := agents.NamedBinding(st.OnEnterAgent); !ok {
					r.Problems = append(r.Problems, fmt.Sprintf(
						"workflow %q node %q sets on_enter_agent=%s with no [%s] binding in agents.toml",
						doc.Name, st.Name, st.OnEnterAgent, st.OnEnterAgent))
				} else if p := performerChannelProblem(doc.Name, st.Name, st.OnEnterAgent, b); p != "" {
					r.Problems = append(r.Problems, p)
				}
			}
		}
		// Edge gates: skills share the edge's agent= binding (default reviewer).
		for _, tr := range spec.Transitions {
			skills := tr.Skills
			if len(skills) == 0 && tr.Skill != "" {
				skills = []string{tr.Skill}
			}
			if len(skills) == 0 {
				continue
			}
			sec := tr.Agent
			if sec == "" {
				sec = "reviewer"
			}
			bm := revModel
			if sec != "reviewer" {
				usedNamed[sec] = true
				if b, ok := agents.NamedBinding(sec); ok {
					bm = b.Model
					if config.ResolvedRole(sec, b) != config.RoleReviewer {
						r.Problems = append(r.Problems, fmt.Sprintf(
							"workflow %q edge %s→%s allocates agent=%s with role=%q (want role=reviewer) — a named performer never advances status",
							doc.Name, tr.From, tr.To, sec, config.ResolvedRole(sec, b)))
					}
				} else {
					r.Problems = append(r.Problems, fmt.Sprintf(
						"workflow %q edge %s→%s allocates agent=%s with no [%s] binding in agents.toml",
						doc.Name, tr.From, tr.To, sec, sec))
				}
			}
			edgeNode := "edge:" + tr.From + "→" + tr.To
			for _, sk := range skills {
				r.Gates = append(r.Gates, gateAlloc(doc.Name, edgeNode, sk, sec, bm))
			}
		}
	}
	for _, name := range sortedNames(agents.Agents) {
		if !usedNamed[name] {
			// Advisory only: a binding may serve a non-workflow verb (e.g.
			// [retrospective] for `satelle story retrospect`) without an agent=
			// node. The satelle-workflow-drift skill judges semantics; validate
			// surfaces the orphan without blocking engage/init.
			r.Warnings = append(r.Warnings, fmt.Sprintf(
				"agents.toml [%s] is orphaned — no workflow node allocates agent=%s (ok if used by a non-workflow verb)",
				name, name))
		}
	}
	return r
}

// checkHooks validates one workflow's LIFECYCLE HOOK allocations and records a
// GateAllocation per hook, so an operation that fires outside the status graph is
// as inspectable and as checkable as a gated edge (sty_ede16f51).
//
// It owns the ALLOCATION half of hook validation — does the named binding exist
// and can it produce a verdict. The DECLARATION half (unknown
// operation, malformed entry, unresolved skill) belongs to
// agentstep.WorkflowConsistency, which has the skill resolver this package
// deliberately does not. `satelle agent validate` runs both, so one command still
// covers the whole surface without either check reporting the other's findings
// twice.
func checkHooks(doc docindex.Doc, agents config.AgentsConfig, revModel string, usedNamed map[string]bool, r *Report) []string {
	hooks, _ := wfhook.Parse(doc.Body)
	var problems []string
	for _, h := range hooks {
		sec := strings.TrimSpace(h.Agent)
		if sec == "" {
			sec = wfhook.DefaultAgent
		}
		b := agents.ReviewerBinding()
		bm := revModel
		if sec != "reviewer" {
			named, ok := agents.NamedBinding(sec)
			if !ok {
				problems = append(problems, fmt.Sprintf(
					"workflow %q hook %s allocates agent=%s with no [%s] binding in agents.toml",
					doc.Name, h.Operation, sec, sec))
				continue
			}
			// A named binding a hook allocates is USED — never report it orphaned.
			usedNamed[sec] = true
			b, bm = named, named.Model
		}
		if h.Verdict {
			// A verdict hook is a gate in everything but position, so it carries a
			// gate's two mechanism requirements. Both are refused HERE, before a
			// story is ever created, rather than only at invocation time.
			if role := config.ResolvedRole(sec, b); role != config.RoleReviewer {
				problems = append(problems, fmt.Sprintf(
					"workflow %q hook %s allocates agent=%s with role=%q (want role=reviewer) — a verdict hook decides accept/reject and a named performer never does",
					doc.Name, h.Operation, sec, role))
			}
			if config.IsInLoopCommand(b.CommandTemplate()) {
				problems = append(problems, fmt.Sprintf(
					"workflow %q hook %s allocates agent=%s with command=in-loop — it cannot produce an isolated verdict; the hook would be refused at invocation",
					doc.Name, h.Operation, sec))
			}
			// The reviewer PERMISSION CEILING is deliberately NOT re-decided here.
			// checkBinding already judges every section's ceiling with the severity
			// split this package has settled on: a provable escape (a Codex danger
			// sandbox, a non-read-only Codex sandbox) is a hard problem, while an
			// unexpressed ceiling is a warning because ReadOnly is a heuristic. The
			// hook's section is one of those sections, so it is already covered —
			// re-checking it here would only re-decide the same heuristic at a
			// harsher severity, and would hard-fail every repo whose reviewer
			// template the heuristic cannot classify.
		}
		alloc := gateAlloc(doc.Name, "hook:"+h.Operation, h.Skill, sec, bm)
		alloc.Operation = h.Operation
		alloc.Source = h.Source
		r.Gates = append(r.Gates, alloc)
	}
	return problems
}

// gateAlloc builds a GateAllocation for the binding that will run the gate.
func gateAlloc(workflow, node, skill, agent, bindingModel string) GateAllocation {
	return GateAllocation{
		Workflow: workflow, Node: node, Skill: skill, Agent: agent,
		BindingModel: bindingModel, EffectiveModel: bindingModel,
	}
}

// checkBinding validates one binding and builds its Grant.
// Returns hard problems and advisory warnings (role inference, role/path mismatch).
// performerChannelProblem returns the finding for a binding a workflow allocates
// to a DISPATCHED performing role whose grant carries no pull-context channel,
// or "" when the allocation is sound (sty_87c0ef37).
//
// This is the same condition internal/agentstep refuses at dispatch time —
// decided by the same config.GrantsContextChannel predicate — surfaced BEFORE a
// story is engaged rather than mid-transition. An in-loop binding is performed by
// the driving session with full context and is never dispatched, so it is exempt.
func performerChannelProblem(workflow, node, section string, b config.AgentBinding) string {
	if config.IsInLoopCommand(b.CommandTemplate()) || config.GrantsContextChannel(b.Tools) {
		return ""
	}
	return fmt.Sprintf(
		"workflow %q node %q allocates performer agent=%s whose agents.toml [%s] tools grant has no context channel — a dispatched agent starts with no history and must pull the story; add `Bash(satelle:*)` for the satelle CLI, or `read_file` for disk reads under ~/.satelle/<repo-key>/stories/<id>/. Dispatch will otherwise be refused",
		workflow, node, section, section)
}

func checkBinding(section string, b config.AgentBinding) (Grant, []string, []string) {
	var problems, warnings []string
	cmd := b.CommandTemplate()
	if cmd == "" {
		// Should not happen after *Binding resolvers, but be defensive.
		if section == "executor" {
			cmd = config.DefaultExecutorCommand
		} else {
			cmd = config.DefaultReviewerCommand
		}
	}

	role := config.ResolvedRole(section, b)
	iface := b.ResolvedInterface()
	g := Grant{
		Name:              section,
		Interface:         iface,
		Command:           cmd,
		Secondary:         b.Secondary,
		Tools:             b.Tools,
		Model:             b.Model,
		Effort:            b.Effort,
		Timeout:           b.Timeout,
		InjectsPrinciples: b.InjectsPrinciples(),
		Role:              role,
		Principles:        b.ResolvedPrinciples(),
		RoleInferred:      config.RoleInferred(b),
		ContextChannel:    config.GrantsContextChannel(b.Tools),
	}
	if g.RoleInferred {
		warnings = append(warnings, fmt.Sprintf(
			"agents.toml [%s] has no role= declared — inferred role=%s; set role = %q to make the contract explicit",
			section, role, role))
	}
	// Role/path mismatch warnings (not hard fails — user may reassign intentionally).
	if section == "reviewer" && role != config.RoleReviewer {
		warnings = append(warnings, fmt.Sprintf(
			"agents.toml [reviewer] resolves role=%s (want role=reviewer for gate verdicts)", role))
	}
	// Named role=reviewer bindings ARE allocatable on gated edges (sty_a476a2f8 /
	// sty_6ab016dc). Do not warn that they are "named perform" or that gates
	// always fall back to [reviewer] — that contradicted the NODE allocation
	// lines and the engine gateBinding path. Orphan detection (unusedNamed)
	// still reports bindings no workflow allocates.
	// In-loop reviewer cannot produce an isolated verdict — warn at validate;
	// gate refuses loud at transition time (design §6.4).
	if role == config.RoleReviewer {
		if config.IsInLoopCommand(cmd) {
			warnings = append(warnings, fmt.Sprintf(
				"agents.toml [%s] is role=reviewer with command=in-loop — cannot produce an isolated verdict; gates will refuse",
				section))
		}
		// Unused capability, not a prohibition (sty_87c0ef37): satelle injects a
		// reviewer's documents into the transition payload's docs array, and
		// reviewer bindings never reach the dispatch path that consults a context
		// channel. A shell grant on a reviewer is therefore never exercised — it
		// only widens the ceiling. Stated as the mechanical fact; whether a repo
		// keeps it is the repo's call, so this is a warning, not a problem.
		if tok := config.ShellGrantToken(b.Tools); tok != "" {
			warnings = append(warnings, fmt.Sprintf(
				"agents.toml [%s] is role=reviewer but grants shell (%s) — reviewers are fed their documents in the transition payload, so the grant is never used and only widens the ceiling",
				section, tok))
		}
	}
	if len(b.Env) > 0 {
		keys := make([]string, 0, len(b.Env))
		for k := range b.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		g.Notes = "env keys: " + strings.Join(keys, ",")
	}

	// Unknown interface (LoadAgents also rejects; keep validate defensive).
	if iface != config.InterfaceCommand && iface != config.InterfaceACP {
		problems = append(problems, fmt.Sprintf(
			"agents.toml [%s] interface %q: want %q or %q",
			section, b.Interface, config.InterfaceCommand, config.InterfaceACP))
		g.Backend = "invalid"
		return g, problems, warnings
	}

	fields := strings.Fields(cmd)
	lower0 := ""
	if len(fields) > 0 {
		lower0 = strings.ToLower(fields[0])
	}

	// ACP transport (epic:agent-dispatch-transport): spawn line only; no argv placeholders.
	if iface == config.InterfaceACP {
		runner, err := agentcli.RunnerFromBinding(iface, cmd)
		if err != nil {
			problems = append(problems, fmt.Sprintf("agents.toml [%s] acp: %v", section, err))
			g.Backend = "invalid"
		} else {
			g.Backend = "acp:" + runner.Name()
			if g.Notes == "" {
				g.Notes = "acp spawn: " + runner.Command()
			} else {
				g.Notes += "; acp spawn: " + runner.Command()
			}
			// Ceiling: tools grant + client permission policy (not argv --deny).
			g.ReadOnly = b.Tools != "" && !toolsGrantMutators(b.Tools)
			if role == config.RoleReviewer {
				if b.Tools == "" {
					problems = append(problems, fmt.Sprintf(
						"agents.toml [%s] interface=acp role=reviewer requires tools= (grant evidence; ACP ceiling is tools + client permission policy, not argv --deny)",
						section))
				} else if !g.ReadOnly {
					warnings = append(warnings, fmt.Sprintf(
						"agents.toml [%s] is role=reviewer with interface=acp and tools that appear to allow mutators (%s) — prefer a read-only tools list; satelle denies mutator ACP tool kinds only when tools look read-only",
						section, b.Tools))
				} else if g.Notes == "" {
					g.Notes = "ceiling: acp permission policy + tools"
				} else {
					g.Notes += "; ceiling: acp permission policy + tools"
				}
			}
		}
		if _, err := b.TimeoutDuration(0); err != nil {
			problems = append(problems, fmt.Sprintf("agents.toml [%s] timeout: %v", section, err))
		}
		return g, problems, warnings
	}

	switch {
	case len(fields) == 0 || lower0 == "in-loop":
		g.Backend = "in-loop"
		g.ReadOnly = false
		if g.Notes == "" {
			g.Notes = "full session grant (driving agent)"
		} else {
			g.Notes += "; full session grant (driving agent)"
		}
	case len(fields) == 1 && (lower0 == agentcli.CLIClaude || lower0 == agentcli.CLIGrok || lower0 == agentcli.CLICodex):
		// Bare CLI presets removed from the agents.toml path — full template required.
		g.Backend = "invalid"
		g.ReadOnly = false
		problems = append(problems, fmt.Sprintf(
			"agents.toml [%s] command %q: bare CLI presets removed — use a full command template or run satelle init to migrate",
			section, fields[0]))
	default:
		runner, err := agentcli.RunnerFromCommand(cmd)
		if err != nil {
			problems = append(problems, fmt.Sprintf("agents.toml [%s] command: %v", section, err))
			g.Backend = "invalid"
			break
		}
		// Isolated multi-token runner — classify backend + read-only ceiling.
		// (Single-token non-in-loop never returns a nil runner without error.)
		name := runner.Name()
		g.Backend = "isolated:" + name
		resolved := runner.Command()
		switch strings.ToLower(name) {
		case agentcli.CLIClaude:
			g.ReadOnly = strings.Contains(resolved, "--disallowedTools")
			if b.Tools == "" {
				g.Tools = config.DefaultReviewerTools
			}
		case agentcli.CLIGrok:
			// Grok full template typically bakes read-only tools + --deny mutators.
			g.ReadOnly = strings.Contains(resolved, "--deny") || strings.Contains(resolved, "read_file")
			if b.Tools == "" && strings.Contains(resolved, "read_file") {
				g.Tools = "read_file,grep,list_dir"
			}
		case agentcli.CLICodex:
			// Codex exec: sandbox mode is the ceiling evidence (sty_3b4909bb).
			g.ReadOnly = commandHasCodexReadOnlySandbox(resolved)
		default:
			// Full template: surface the command so the ceiling is visible.
			g.ReadOnly = strings.Contains(resolved, "--disallowedTools") ||
				strings.Contains(resolved, "--deny") ||
				commandHasCodexReadOnlySandbox(resolved) ||
				(strings.Contains(resolved, "Read") && !strings.Contains(resolved, "Write"))
		}
		// Placeholder completeness (sty_21db3670): buildArgs substitutes only
		// tokens that equal {system} verbatim, so a multi-token isolated command
		// without that token runs with no gate/skill rubric. Hard-fail.
		if !hasToken(fields, "{system}") {
			problems = append(problems, fmt.Sprintf(
				"agents.toml [%s] command omits {system} as its own argv token — the gate/skill rubric is never appended and the agent runs without its rubric",
				section))
		}
		// Hard-reject danger sandbox for role=reviewer (sty_3b4909bb AC3) — not a
		// warning. danger-full-access / --dangerously-bypass-* erase the ceiling.
		codexCmd := isCodexCommand(name, resolved)
		dangerHard := role == config.RoleReviewer && commandHasDangerSandbox(resolved)
		if dangerHard {
			problems = append(problems, fmt.Sprintf(
				"agents.toml [%s] is role=reviewer with a command that disables the sandbox ceiling (%s) — refuse; use -s read-only (DefaultCodexExecCommand) or a non-danger template",
				section, dangerSandboxToken(resolved)))
		}
		// Codex command reviewers must have effective sandbox read-only
		// (sty_aa726901 AC3): workspace-write or omitted sandbox hard-reject.
		// Danger already failed above — do not double-report. ACP branch returns
		// earlier; Codex ACP uses tools grant, not -s.
		codexSandboxHard := false
		if role == config.RoleReviewer && codexCmd && !commandHasCodexReadOnlySandbox(resolved) && !dangerHard {
			codexSandboxHard = true
			problems = append(problems, fmt.Sprintf(
				"agents.toml [%s] is role=reviewer with a Codex command whose effective sandbox is %q (want -s read-only) — refuse; use DefaultCodexExecCommand",
				section, effectiveCodexSandbox(resolved)))
		}
		// Reviewer read-only ceiling: advisory when role=reviewer but no ceiling
		// is expressed (no --disallowedTools/--deny / read-only heuristic miss).
		// Warn not fail for non-Codex templates — g.ReadOnly is a heuristic.
		// Codex non-RO already produced a Problem above (sty_aa726901).
		if role == config.RoleReviewer && !g.ReadOnly && !dangerHard && !codexSandboxHard {
			warnings = append(warnings, fmt.Sprintf(
				"agents.toml [%s] is role=reviewer with an isolated command that expresses no read-only ceiling (no --disallowedTools/--deny of mutators, no -s read-only) — the reviewer could silently gain write; deny the mutators or use the default claude/grok/codex template",
				section))
		}
		if g.Notes == "" {
			g.Notes = "command: " + resolved
		} else {
			g.Notes += "; command: " + resolved
		}
	}

	if _, err := b.TimeoutDuration(0); err != nil {
		problems = append(problems, fmt.Sprintf("agents.toml [%s] timeout: %v", section, err))
	}
	return g, problems, warnings
}

// hasToken reports whether tok appears as its own element of fields (exact match).
// Used for the {system} completeness check: the gate rubric must be its own argv
// token. agentcli.buildArgs also supports fused {model}/{effort}/{settings}
// (sty_aa726901); those do not apply to {system}, which stays exact-token-only.
func hasToken(fields []string, tok string) bool {
	for _, f := range fields {
		if f == tok {
			return true
		}
	}
	return false
}

// isCodexCommand reports a Codex command-transport template (sty_aa726901 AC3):
// binary name is codex, or the resolved line contains "codex exec".
func isCodexCommand(name, resolved string) bool {
	if strings.EqualFold(name, agentcli.CLICodex) {
		return true
	}
	return strings.Contains(strings.ToLower(resolved), "codex exec")
}

// commandHasCodexReadOnlySandbox reports Codex-style read-only sandbox ceiling
// evidence in a resolved command template (sty_3b4909bb).
func commandHasCodexReadOnlySandbox(resolved string) bool {
	return effectiveCodexSandbox(resolved) == "read-only"
}

// effectiveCodexSandbox returns the sandbox mode token for a Codex-style command
// template: "read-only", "workspace-write", "danger-full-access", or "none (omitted)"
// (sty_aa726901 AC3). Prefer explicit -s/--sandbox/sandbox= over danger markers.
func effectiveCodexSandbox(resolved string) string {
	lower := strings.ToLower(resolved)
	// sandbox=VALUE fused form
	if i := strings.Index(lower, "sandbox="); i >= 0 {
		rest := lower[i+len("sandbox="):]
		// token until space
		end := strings.IndexAny(rest, " \t")
		val := rest
		if end >= 0 {
			val = rest[:end]
		}
		if val != "" {
			return val
		}
	}
	fields := strings.Fields(lower)
	for i, f := range fields {
		if (f == "-s" || f == "--sandbox") && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	if strings.Contains(lower, "danger-full-access") {
		return "danger-full-access"
	}
	return "none (omitted)"
}

// commandHasDangerSandbox reports Codex (or similar) danger sandbox tokens that
// erase a reviewer ceiling (sty_3b4909bb AC3).
func commandHasDangerSandbox(resolved string) bool {
	lower := strings.ToLower(resolved)
	if strings.Contains(lower, "danger-full-access") {
		return true
	}
	if strings.Contains(lower, "--dangerously-bypass-approvals-and-sandbox") {
		return true
	}
	if strings.Contains(lower, "--dangerously-bypass-hook-trust") {
		// Hook trust bypass alone is not a full sandbox erase; do not hard-fail.
		return false
	}
	return false
}

// dangerSandboxToken returns a short label for the first danger marker found.
func dangerSandboxToken(resolved string) string {
	lower := strings.ToLower(resolved)
	switch {
	case strings.Contains(lower, "danger-full-access"):
		return "danger-full-access"
	case strings.Contains(lower, "--dangerously-bypass-approvals-and-sandbox"):
		return "--dangerously-bypass-approvals-and-sandbox"
	default:
		return "danger sandbox"
	}
}

// toolsGrantMutators mirrors agentcli.toolsAllowMutators for validate-time
// ceiling evidence (keep logic aligned: write/edit/shell without satelle-only).
func toolsGrantMutators(tools string) bool {
	t := strings.ToLower(tools)
	for _, needle := range []string{"write", "edit", "search_replace", "multiedit", "notebookedit", "run_terminal"} {
		if strings.Contains(t, needle) {
			return true
		}
	}
	if strings.Contains(t, "bash") && !strings.Contains(t, "satelle") {
		return true
	}
	return false
}

func sortedNames(m map[string]config.AgentBinding) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
