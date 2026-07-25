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
)

// Grant is one agent's resolved, inspectable capability surface — what validate
// surfaces so a preset's baked grant is visible without knowing the expansion.
// Env VALUES are never included (secrets); key names may appear in Notes.
type Grant struct {
	Name              string
	Backend           string // in-loop | isolated:claude | isolated:grok | isolated:<binary> | acp:<binary> | codex (unmapped)
	Interface         string // command | acp (epic:agent-dispatch-transport)
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
}

// GateAllocation is one workflow gate/node's resolved binding (sty_a476a2f8).
// EffectiveModel is the binding's model — agents.toml owns model, not the DOT.
type GateAllocation struct {
	Workflow       string
	Node           string // state name, or "edge:from→to" for edge gates
	Skill          string
	Agent          string // binding section that will run the gate
	BindingModel   string
	EffectiveModel string // same as BindingModel (no DOT override)
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
}

// OK reports whether the report carries no hard problems.
func (r Report) OK() bool { return len(r.Problems) == 0 }

// Validate checks every agents.toml binding and each workflow's agent= node
// allocations. vars is the [vars] KV used to resolve ${VAR} in binding env/
// settings (may be nil). workflows may be empty (agent-only check).
func Validate(agents config.AgentsConfig, vars map[string]string, workflows []docindex.Doc) Report {
	var r Report

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
		r.Grants = append(r.Grants, g)
		r.Problems = append(r.Problems, problems...)
		r.Warnings = append(r.Warnings, warnings...)
	}

	// Workflow node → binding + orphan named bindings + per-gate effective model.
	usedNamed := map[string]bool{}
	revModel := agents.ReviewerBinding().Model
	for _, doc := range workflows {
		spec, ok := wfdot.Parse(doc.Body)
		if !ok {
			continue // structure.Doc / workflow validate owns unparseable bodies
		}
		for _, st := range spec.States {
			// Plan D2 / sty_a476a2f8: on= marks a scoped GATE (judge); spine
			// (no on=) with agent=<name> is a PERFORMER. Split before role checks.
			if st.Skill != "" && len(st.On) > 0 && st.Skill != wfdot.StepSummarySkill {
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
					r.Gates = append(r.Gates, gateAlloc(doc.Name, st.Name, st.Skill, st.Agent, b.Model))
				}
			}
			// on_enter_agent=<name> one-shot entry performer (sty_5cabe26f) —
			// orthogonal to agent=; must also resolve to a binding and counts
			// as a use so the binding is not flagged orphaned.
			if st.OnEnterAgent != "" {
				usedNamed[st.OnEnterAgent] = true
				if _, ok := agents.NamedBinding(st.OnEnterAgent); !ok {
					r.Problems = append(r.Problems, fmt.Sprintf(
						"workflow %q node %q sets on_enter_agent=%s with no [%s] binding in agents.toml",
						doc.Name, st.Name, st.OnEnterAgent, st.OnEnterAgent))
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

// gateAlloc builds a GateAllocation for the binding that will run the gate.
func gateAlloc(workflow, node, skill, agent, bindingModel string) GateAllocation {
	return GateAllocation{
		Workflow: workflow, Node: node, Skill: skill, Agent: agent,
		BindingModel: bindingModel, EffectiveModel: bindingModel,
	}
}

// checkBinding validates one binding and builds its Grant.
// Returns hard problems and advisory warnings (role inference, role/path mismatch).
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
		Tools:             b.Tools,
		Model:             b.Model,
		Effort:            b.Effort,
		Timeout:           b.Timeout,
		InjectsPrinciples: b.InjectsPrinciples(),
		Role:              role,
		Principles:        b.ResolvedPrinciples(),
		RoleInferred:      config.RoleInferred(b),
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
		fields0 := strings.Fields(cmd)
		if len(fields0) == 1 && strings.EqualFold(fields0[0], "in-loop") {
			warnings = append(warnings, fmt.Sprintf(
				"agents.toml [%s] is role=reviewer with command=in-loop — cannot produce an isolated verdict; gates will refuse",
				section))
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
		default:
			// Full template: surface the command so the ceiling is visible.
			g.ReadOnly = strings.Contains(resolved, "--disallowedTools") ||
				strings.Contains(resolved, "--deny") ||
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
		// Reviewer read-only ceiling: advisory when role=reviewer but no ceiling
		// is expressed (no --disallowedTools/--deny / read-only heuristic miss).
		// Warn not fail — g.ReadOnly is a heuristic and a legitimate ceiling form
		// it misses must not hard-block engage (AC2).
		if role == config.RoleReviewer && !g.ReadOnly {
			warnings = append(warnings, fmt.Sprintf(
				"agents.toml [%s] is role=reviewer with an isolated command that expresses no read-only ceiling (no --disallowedTools/--deny of mutators) — the reviewer could silently gain write; deny the mutators or use the default claude/grok template",
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
// Mirrors agentcli.buildArgs placeholder substitution, which only substitutes a
// token that equals the placeholder verbatim — not a fused substring.
func hasToken(fields []string, tok string) bool {
	for _, f := range fields {
		if f == tok {
			return true
		}
	}
	return false
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
