// Package agentvalidate is the store-free, deterministic check of the agents
// layer (.satelle/workflows/agents.toml) and each workflow's agent= node bindings.
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
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/wfhook"
	"github.com/bobmcallan/satelle/internal/wfroute"
)

// Grant is one agent's resolved, inspectable capability surface — what validate
// surfaces so a preset's baked grant is visible without knowing the expansion.
// Env VALUES are never included (secrets); key names may appear in Notes.
type Grant struct {
	Name      string
	Backend   string // in-loop | isolated:claude | isolated:grok | isolated:codex | isolated:<binary> | acp:<binary> | stream:<binary>
	Interface string // command | acp | stream (epic:agent-dispatch-transport)
	// InterfaceReason states why Interface resolved the way it did:
	// "explicit" (the binding set interface= itself), "one-shot default" (a
	// gate/planner/advisor dispatch always resolves to command), "live use"
	// (a rework seat, rework.consult, or story-chat/orchestrator binding
	// resolved to its CLI's best live transport), "live use: in-loop" (a live
	// use whose command is the in-loop preset, which has no live transport),
	// or "live use: not live-capable" (a live use where no configured live
	// transport could actually open the command) — config.AgentsConfig.
	// ResolveInterface's own reason (epic:model-selection child 2).
	InterfaceReason string
	// Command is the effective command template — the literal argv the operator
	// can read. Surfaced as a field (not only inside Notes) so a provenance
	// display can attribute it like any other resolved value (sty_c7dfeedf).
	Command string
	// Secondary is the rate-limit failover binding name (sty_5bf61f89), empty
	// when unconfigured.
	Secondary string
	// IdleTimeout is the raw idle_timeout= (sty_752c4ef2), empty when the
	// binding inherits [defaults]/the shipped default.
	IdleTimeout string
	// BusyTimeout is the raw busy_timeout= (sty_db62a3b9), empty when unset.
	BusyTimeout       string
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
// EffectiveModel is what actually dispatches (sty_7069bced): the binding's own
// model= wins outright; a step's model= is a per-dispatch override reported
// only for a spine PERFORMER node (a gate/edge has no step tier of its own —
// the retired DOT edge model=, sty_a476a2f8, stays retired). ModelSource names
// which of the two supplied it, empty when neither did (the inherited/creator/
// cli-default tiers are runtime-only — they depend on the story's sessions and
// are not knowable at static validate time).
type GateAllocation struct {
	Workflow       string
	Node           string // state name, "edge:from→to" for edge gates, or "hook:<operation>"
	Skill          string
	Agent          string // binding section that will run the gate
	BindingModel   string
	EffectiveModel string // BindingModel, else the step's model= override
	// ModelSource is config.ModelSourceBinding, config.ModelSourceStep, or ""
	// (neither set — validate cannot know the runtime-resolved tiers).
	ModelSource string
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
	// Findings is the same set of observations as Problems/Warnings, carrying a
	// STABLE identifier and remediation each (sty_e9da28e2). Problems/Warnings
	// remain the prose surfaces older callers print; every finding's Detail is
	// the very string that appears there, so the two cannot drift.
	Findings health.Findings
}

// record appends findings and mirrors them into the prose lists, so a caller
// that reads Problems/Warnings and one that reads Findings always agree.
func (r *Report) record(fs ...health.Finding) {
	for _, f := range fs {
		r.Findings = append(r.Findings, f)
		switch f.Severity {
		case health.SeverityError:
			r.Problems = append(r.Problems, f.Detail)
		case health.SeverityWarn:
			r.Warnings = append(r.Warnings, f.Detail)
		}
	}
}

// allocProblem records a workflow node/edge allocation defect — a node naming a
// binding that does not exist, or one whose role cannot do the job.
func (r *Report) allocProblem(msg string) {
	r.record(health.Error(health.IDNodeAlloc, "Unusable workflow allocation", msg).
		WithRemediation("add the named binding to .satelle/workflows/agents.toml, or change the workflow's agent="))
}

// tagged records already-authored prose under one stable id — used where a whole
// region of checks shares a defect class (a node allocation, a hook allocation).
// The id comes from the SITE, never from matching the message text.
func (r *Report) tagged(id, title, remediation string, sev health.Severity, msgs ...string) {
	for _, m := range msgs {
		r.record(health.Finding{ID: id, Severity: sev, Title: title, Detail: m, Remediation: remediation})
	}
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
	return validateEffective(repo, config.AgentsConfig{}, global, repoVars, workflows, nil)
}

// ValidateEffectiveLayered is ValidateEffectiveWithSkills with the synced
// workspace bindings layer in the ladder (sty_01949949), so what is judged —
// and the provenance each grant reports — is what will actually run once a
// repo has pulled workspace bindings. Surfaces that read a deployed repo from
// disk (doctor, `satelle agent validate`) pass config.LoadWorkspaceAgents.
func ValidateEffectiveLayered(repo, workspace config.AgentsConfig, global config.GlobalAgentsConfig, repoVars map[string]string, workflows []docindex.Doc, skills SkillBody) Report {
	baseline, err := config.BaselineAgents()
	if err != nil {
		f := health.Error(health.IDAgentsLoad, "Unreadable baseline agent seats", err.Error())
		r := validate(repo, config.LayerVars(global.Vars, repoVars), workflows, nil, skills)
		r.Problems = append([]string{f.Detail}, r.Problems...)
		r.Findings = append(health.Findings{f}, r.Findings...)
		return r
	}
	return validateEffectiveBaseline(baseline, repo, workspace, global, repoVars, workflows, skills)
}

func validateEffective(repo, workspace config.AgentsConfig, global config.GlobalAgentsConfig, repoVars map[string]string, workflows []docindex.Doc, skills SkillBody) Report {
	return validateEffectiveBaseline(config.AgentsConfig{}, repo, workspace, global, repoVars, workflows, skills)
}

// validateEffectiveBaseline judges the layers as LoadEffectiveAgents resolves
// them, with the embedded baseline seats beneath the repo's file.
func validateEffectiveBaseline(baseline, repo, workspace config.AgentsConfig, global config.GlobalAgentsConfig, repoVars map[string]string, workflows []docindex.Doc, skills SkillBody) Report {
	agents, prov, err := config.ResolveAgentsBaseline(baseline, repo, workspace, global)
	eff := config.EffectiveAgents{Agents: agents, Provenance: prov, Vars: config.LayerVars(global.Vars, repoVars)}
	if err != nil {
		r := validate(repo, config.LayerVars(global.Vars, repoVars), workflows, nil, skills)
		f := health.Error(health.IDAgentsProfileBroken, "Broken machine-wide profile reference", err.Error()).
			WithRemediation("fix the profile= reference in .satelle/workflows/agents.toml, or the profile in " + config.GlobalAgentsLabel)
		r.Problems = append([]string{f.Detail}, r.Problems...)
		r.Findings = append(health.Findings{f}, r.Findings...)
		return r
	}
	// A seat only the baseline names is shipped, not authored by this repo: the
	// shipped route decides whether a node uses it, so it is never an orphan here.
	shipped := map[string]bool{}
	for n := range baseline.Agents {
		if _, ok := repo.Agents[n]; !ok {
			if _, ok := workspace.Agents[n]; !ok {
				shipped[n] = true
			}
		}
	}
	return validateShipped(eff.Agents, eff.Vars, workflows, eff.Provenance, skills, shipped)
}

// Validate checks every agents.toml binding and each workflow's agent= node
// allocations. vars is the [vars] KV used to resolve ${VAR} in binding env/
// settings (may be nil). workflows may be empty (agent-only check). Callers that
// have already resolved the machine-wide catalog pass the effective layer here;
// ValidateEffective is the form that resolves it and reports provenance.
func Validate(agents config.AgentsConfig, vars map[string]string, workflows []docindex.Doc) Report {
	return validate(agents, vars, workflows, nil, nil)
}

// SkillBody reads a skill's markdown body by name, reporting whether it
// resolved. It is the ONE thing a caller can supply that this package cannot
// derive from its arguments: whether a reviewer's rubric shells `satelle`, and
// therefore whether a shell grant is live (sty_338a53f8).
//
// It is OPTIONAL. A nil resolver means "no skill bodies available", and every
// check behaves exactly as it did before — a caller that only has the agents
// layer in hand keeps working, it just cannot see that evidence.
type SkillBody func(name string) (string, bool)

// ValidateEffectiveWithSkills is ValidateEffective plus the skill-body resolver.
// Surfaces that judge a whole DEPLOYED repo (doctor, and therefore `satelle
// init`, and `satelle agent validate`) pass one; the narrower callers do not.
func ValidateEffectiveWithSkills(repo config.AgentsConfig, global config.GlobalAgentsConfig, repoVars map[string]string, workflows []docindex.Doc, skills SkillBody) Report {
	return validateEffective(repo, config.AgentsConfig{}, global, repoVars, workflows, skills)
}

func validate(agents config.AgentsConfig, vars map[string]string, workflows []docindex.Doc, prov config.Provenance, skills SkillBody) Report {
	return validateShipped(agents, vars, workflows, prov, skills, nil)
}

// validateShipped is validate with the set of baseline-only seats, which are
// exempt from the orphan advisory.
func validateShipped(agents config.AgentsConfig, vars map[string]string, workflows []docindex.Doc, prov config.Provenance, skills SkillBody, shipped map[string]bool) Report {
	var r Report
	r.Provenance = prov

	// Env/settings resolution once — fail-fast naming section+key, never values.
	if _, err := config.ResolveAgentEnvs(agents, vars); err != nil {
		r.record(health.Error(health.IDEnvUnresolved, "Unresolved variable", err.Error()).
			WithRemediation("define the named key under [vars] in satelle.local.toml (secrets) or satelle.toml"))
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

	// A binding used LIVE (a rework relay seat, rework.consult, or the
	// orchestrator/story-chat binding) resolves an unset interface= to its
	// CLI's best live transport; everything else resolves one-shot to command
	// (epic:model-selection child 2). ResolveInterface needs the RAW binding
	// to tell "no command yet" from "already claude-shaped" — sec.b above is
	// already command-defaulted by NamedBinding/*Binding for dispatch, so the
	// reason/effective-command are computed from the raw form. For a LIVE use,
	// that raw form must be LiveRawBinding, not the bare RawBinding: "executor"
	// and "reviewer" carry their own role default (in-loop / DefaultReviewerTools)
	// that only LiveRawBinding applies, and this must compute from the SAME
	// starting point LiveBinding (what OpenSessionAs actually opens) uses, or
	// this report and the runtime disagree (sty_119f6fda).
	live := liveUsedSections(workflows)
	for _, sec := range sections {
		use := config.UseOneShot
		if live[sec.name] {
			use = config.UseLive
		}
		b := sec.b
		iface, reason := b.ResolvedInterface(), "one-shot default"
		raw, ok := agents.RawBinding(sec.name)
		if ok && use == config.UseLive {
			raw, ok = agents.LiveRawBinding(sec.name)
		}
		if ok {
			iface, reason = agents.ResolveInterface(raw, use)
			if use == config.UseLive {
				b = agents.EffectiveBinding(raw, use)
				if strings.TrimSpace(b.Command) == "" {
					r.record(health.Warn(health.IDAgentsBinding, "Live binding has no command",
						config.LiveCommandRequiredError(sec.name).Error()).
						About(sec.name).WithRemediation("set command= or profile= on [" + sec.name + "] in .satelle/workflows/agents.toml"))
				}
			}
		}
		g, _, _, fs := checkBinding(sec.name, b, vars)
		g.Interface = iface
		g.InterfaceReason = reason
		g.Sources = prov[sec.name]
		r.Grants = append(r.Grants, g)
		r.record(fs...)
	}

	// Workflow node → binding + orphan named bindings + per-gate effective model.
	usedNamed := map[string]bool{}
	// advisors accumulate across every governed route so the shell-grant check
	// below sees an advisor's rubric as a reachable skill too.
	var advisors []wfroute.Advisor
	revModel := agents.ReviewerBinding().Model
	// A DERIVED route has no per-workflow DOT, so its allocations would go
	// unchecked if this loop only read authored graphs. Expand it into one
	// pseudo-workflow per declared category, so every agent= the route names is
	// validated exactly as an authored node's would be (sty_9835070d).
	for _, doc := range expandRouteSources(workflows) {
		// Lifecycle hooks FIRST: they are frontmatter, so they must be checked even
		// when the DOT below does not parse (sty_ede16f51).
		r.tagged(health.IDHookAlloc, "Unusable lifecycle-hook allocation",
			"declare an isolated role=\"reviewer\" binding for the hook's agent in .satelle/workflows/agents.toml",
			health.SeverityError, checkHooks(doc.Doc, agents, revModel, usedNamed, &r)...)

		// ADVISORS are a USAGE signal and nothing more (sty_338a53f8). A route that
		// names an agent as its park advisor or a step's `advise` is allocating that
		// binding just as surely as a node's agent= does, so it must not be reported
		// orphaned. It is deliberately NOT checked for role or context channel: an
		// advisor is consulted by the ORCHESTRATOR and never dispatched
		// (internal/wfdot/route.go — "declared, never dispatched"), so it carries
		// none of a performer's dispatch obligations. Do not "complete" this check.
		for _, ad := range doc.advisors {
			if ad.Agent != "" && ad.Agent != "reviewer" && ad.Agent != "executor" {
				usedNamed[ad.Agent] = true
			}
			advisors = append(advisors, ad)
		}

		// REWORK consult bindings are a usage signal like an advisor's, and they
		// carry ONE obligation an advisor does not: the relay opens them as a LIVE
		// session, so a `command`/in-loop binding cannot serve. Both checks are
		// WARN, never a refusal — a repo may author the loop before wiring the
		// binding, and the loop is opened by hand, so a broken one costs a clear
		// error at `satelle story rework` rather than a broken gate (sty_8e0b29a0).
		for _, w := range doc.reworks {
			if w.Consult == "" {
				continue
			}
			if w.Consult != "reviewer" && w.Consult != "executor" {
				usedNamed[w.Consult] = true
			}
			b, found := agents.LiveBinding(w.Consult)
			if !found {
				r.record(health.Warn(health.IDNodeAlloc, "Rework consult binding missing", fmt.Sprintf(
					"workflow %q step %q declares rework consult=%s rounds=%d with no [%s] binding in agents.toml — satelle story rework cannot open it",
					doc.Name, w.Step, w.Consult, w.Rounds, w.Consult)).
					WithRemediation("add a live-capable [" + w.Consult + "] binding (interface=acp or stream) to .satelle/workflows/agents.toml, or drop the rework key"))
				continue
			}
			if reason := notLiveCapable(w.Consult, b); reason != "" {
				r.record(health.Warn(health.IDNodeAlloc, "Rework consult binding not live-capable", fmt.Sprintf(
					"workflow %q step %q declares rework consult=%s rounds=%d but %s — the rework relay opens it as a live session",
					doc.Name, w.Step, w.Consult, w.Rounds, reason)).About(w.Consult).
					WithRemediation("set interface=acp or interface=stream on [" + w.Consult + "]"))
			}
		}

		spec, ok := doc.spec()
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
							r.allocProblem(fmt.Sprintf(
								"workflow %q node %q allocates agent=%s with role=%q on a step-summary node (want role=reviewer)",
								doc.Name, st.Name, sec, config.ResolvedRole(sec, b)))
						}
					} else {
						r.allocProblem(fmt.Sprintf(
							"workflow %q node %q allocates agent=%s with no [%s] binding in agents.toml",
							doc.Name, st.Name, sec, sec))
					}
				}
				r.Gates = append(r.Gates, gateAlloc(doc.Name, st.Name, st.Skill, sec, bm, ""))
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
								r.allocProblem(fmt.Sprintf(
									"workflow %q node %q allocates agent=%s with role=%q on a gated node (want role=reviewer)",
									doc.Name, st.Name, sec, config.ResolvedRole(sec, b)))
							}
						} else {
							r.allocProblem(fmt.Sprintf(
								"workflow %q node %q allocates agent=%s with no [%s] binding in agents.toml",
								doc.Name, st.Name, sec, sec))
						}
					}
					r.Gates = append(r.Gates, gateAlloc(doc.Name, st.Name, st.Skill, sec, bm, ""))
				}
			} else if st.Agent != "" && st.Agent != "executor" && st.Agent != "reviewer" && len(st.On) == 0 {
				// Named PERFORMER on a spine node (no on=).
				usedNamed[st.Agent] = true
				b, ok := agents.NamedBinding(st.Agent)
				if !ok {
					r.allocProblem(fmt.Sprintf(
						"workflow %q node %q allocates agent=%s with no [%s] binding in agents.toml",
						doc.Name, st.Name, st.Agent, st.Agent))
				} else {
					if config.ResolvedRole(st.Agent, b) == config.RoleReviewer {
						r.allocProblem(fmt.Sprintf(
							"workflow %q node %q allocates agent=%s with role=reviewer on a performing node — use a gated edge or scoped on= node for judges",
							doc.Name, st.Name, st.Agent))
					}
					if p := performerChannelProblem(doc.Name, st.Name, st.Agent, b); p != "" {
						r.allocProblem(p)
					}
					r.Gates = append(r.Gates, gateAlloc(doc.Name, st.Name, st.Skill, st.Agent, b.Model, st.Model))
				}
			}
			// on_enter_agent was validated here as a one-shot entry performer. Flat
			// dispatch retired it (sty_05a5e203): no node dispatches on entry, so
			// there is no entry binding to resolve. An ADVISOR the orchestrator
			// consults is declared on the route, not on a node, and is not a
			// dispatch target — so it carries no context-channel requirement here.
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
						r.allocProblem(fmt.Sprintf(
							"workflow %q edge %s→%s allocates agent=%s with role=%q (want role=reviewer) — a named performer never advances status",
							doc.Name, tr.From, tr.To, sec, config.ResolvedRole(sec, b)))
					}
				} else {
					r.allocProblem(fmt.Sprintf(
						"workflow %q edge %s→%s allocates agent=%s with no [%s] binding in agents.toml",
						doc.Name, tr.From, tr.To, sec, sec))
				}
			}
			edgeNode := "edge:" + tr.From + "→" + tr.To
			for _, sk := range skills {
				r.Gates = append(r.Gates, gateAlloc(doc.Name, edgeNode, sk, sec, bm, ""))
			}
		}
	}
	// Reviewer SHELL GRANT: unused capability, not a prohibition (sty_87c0ef37).
	// satelle injects a reviewer's documents into the transition payload's docs
	// array, and reviewer bindings never reach the dispatch path that consults a
	// context channel — so a shell grant is normally never exercised and only
	// widens the ceiling. Whether a repo keeps it is the repo's call, so this is a
	// warning, not a problem.
	//
	// It is raised HERE rather than in checkBinding because "never used" is a
	// claim about the whole system, not about one binding (sty_338a53f8): a
	// reviewer SKILL the workflow reaches may shell `satelle` in its own body, and
	// then the grant is exactly what makes that skill work. Saying otherwise sent
	// operators to delete a live grant and blind their gates.
	for _, sec := range sections {
		if config.ResolvedRole(sec.name, sec.b) != config.RoleReviewer {
			continue
		}
		tok := config.ShellGrantToken(sec.b.Tools)
		if tok == "" || shellGrantExercised(sec.name, r.Gates, advisors, skills) {
			continue
		}
		r.record(health.Warn(health.IDReviewerUnsafe, "Reviewer ceiling not expressed", fmt.Sprintf(
			"agents.toml [%s] is role=reviewer but grants shell (%s) — reviewers are fed their documents in the transition payload, so the grant is never used and only widens the ceiling",
			sec.name, tok)).About(sec.name))
	}

	for _, name := range sortedNames(agents.Agents) {
		if !usedNamed[name] && !shipped[name] {
			// Advisory only: a binding may serve a non-workflow verb (e.g.
			// [retrospective] for `satelle story retrospect`) without an agent=
			// node. The satelle-workflow-drift skill judges semantics; validate
			// surfaces the orphan without blocking engage/init.
			r.record(health.Warn(health.IDNodeAlloc, "Orphaned binding", fmt.Sprintf(
				"agents.toml [%s] is orphaned — no workflow node allocates agent=%s (ok if used by a non-workflow verb)",
				name, name)))
		}
	}
	return r
}

// orchestratorSection is the binding name `satelle story chat` opens by
// default (agentstep.ChatSessionBinding("")) — always a live use, whether or
// not any workflow names it (it is consumed by a VERB, not a node).
const orchestratorSection = "orchestrator"

// liveUsedSections returns the binding names used as a LIVE session across the
// given workflows — validate's own usage signal for
// config.AgentsConfig.ResolveInterface's `use` parameter (epic:model-selection
// child 2), not a general "is this binding live" authority:
//   - a rework's consult= binding (the relay opens it live to converge),
//   - the agent= seat on the step that declares the rework (the relay's coder
//     seat, opened live alongside its consultant),
//   - "orchestrator", always — `satelle story chat` opens it live regardless
//     of workflow wiring.
//
// Everything else in the agents layer resolves one-shot.
func liveUsedSections(workflows []docindex.Doc) map[string]bool {
	live := map[string]bool{orchestratorSection: true}
	for _, doc := range expandRouteSources(workflows) {
		if len(doc.reworks) == 0 {
			continue
		}
		for _, w := range doc.reworks {
			if w.Consult != "" {
				live[w.Consult] = true
			}
		}
		spec, ok := doc.spec()
		if !ok {
			continue
		}
		agentOf := make(map[string]string, len(spec.States))
		for _, st := range spec.States {
			agentOf[st.Name] = st.Agent
		}
		for _, w := range doc.reworks {
			if agent := agentOf[w.Step]; agent != "" {
				live[agent] = true
			}
		}
	}
	return live
}

// notLiveCapable reports, in prose, why a binding cannot be opened as a live
// session — "" when it can. It asks the runtime's OWN two questions in the
// runtime's order (agentstep.OpenSessionAs): is the command in-loop, and does
// the interface resolve to a session opener. Validate and the relay therefore
// cannot disagree about what "live-capable" means (sty_8e0b29a0).
func notLiveCapable(name string, b config.AgentBinding) string {
	if config.IsInLoopCommand(b.CommandTemplate()) {
		return fmt.Sprintf("[%s] is command=in-loop — the hook channel cannot be relayed", name)
	}
	if strings.TrimSpace(b.Command) == "" {
		return config.LiveCommandRequiredError(name).Error()
	}
	if _, err := agentcli.OpenerFromBinding(b.ResolvedInterface(), b.CommandTemplate()); err != nil {
		return fmt.Sprintf("[%s] interface=%s cannot open a session: %v", name, b.ResolvedInterface(), err)
	}
	return ""
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
		alloc := gateAlloc(doc.Name, "hook:"+h.Operation, h.Skill, sec, bm, "")
		alloc.Operation = h.Operation
		alloc.Source = h.Source
		r.Gates = append(r.Gates, alloc)
	}
	return problems
}

// shellGrantExercised reports whether a section's shell grant is actually USED:
// whether any skill the workflow reaches under that binding — a gate, a scoped
// node rubric, a lifecycle hook, or an advisor's rubric — shells `satelle` in
// its own body (sty_338a53f8).
//
// With no resolver it answers false, which is the pre-existing behaviour: no
// evidence of use, so the advisory stands.
func shellGrantExercised(section string, gates []GateAllocation, advisors []wfroute.Advisor, skills SkillBody) bool {
	if skills == nil {
		return false
	}
	shells := func(name string) bool {
		if name == "" {
			return false
		}
		body, ok := skills(name)
		return ok && skillShellsSatelle(body)
	}
	for _, g := range gates {
		if g.Agent == section && shells(g.Skill) {
			return true
		}
	}
	for _, ad := range advisors {
		if ad.Agent == section && shells(ad.Skill) {
			return true
		}
	}
	return false
}

// skillShellsSatelle reports whether a skill body invokes the satelle CLI.
//
// A heuristic, in the same register as the read-only ceiling test, and it reads
// the body as the MARKDOWN it is: the invocation must sit in a code position —
// inside a fenced or indented code block, in a code span, or after a shell
// prompt. A skill that merely names satelle in a sentence ("satelle runs this
// repo's workflow") is not shelling it, and a plain substring search called
// every such body a shell user.
//
// It only ever SUPPRESSES an advisory, never raises one, so erring toward a
// match costs a warning and never a false failure.
func skillShellsSatelle(body string) bool {
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		// A code span carries the command inline, anywhere on the line.
		if strings.Contains(line, "`satelle ") {
			return true
		}
		// Code position: inside a fence, an indented code block, or after a
		// shell prompt marker.
		cmd := strings.TrimLeft(trimmed, "$>")
		cmd = strings.TrimLeft(cmd, " \t")
		if !strings.HasPrefix(cmd, "satelle ") {
			continue
		}
		if inFence || trimmed != cmd || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			return true
		}
	}
	return false
}

// gateAlloc builds a GateAllocation for the binding that will run the gate.
// stepModel is the step's own model= (empty for a gate/edge/hook node, which
// has no step tier — sty_7069bced).
func gateAlloc(workflow, node, skill, agent, bindingModel, stepModel string) GateAllocation {
	effective, source := bindingModel, ""
	switch {
	case strings.TrimSpace(bindingModel) != "":
		source = config.ModelSourceBinding
	case strings.TrimSpace(stepModel) != "":
		effective, source = stepModel, config.ModelSourceStep
	}
	return GateAllocation{
		Workflow: workflow, Node: node, Skill: skill, Agent: agent,
		BindingModel: bindingModel, EffectiveModel: effective, ModelSource: source,
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

// checkBinding validates one binding and builds its Grant. It reports the same
// prose it always has, and records each observation as a health.Finding under a
// stable id (sty_e9da28e2): every defect gets the SAME identifier wherever it
// surfaces — agent validate, init, doctor, or an engage refusal. The id is
// chosen by the SITE that produced the finding, never by matching its text.
func checkBinding(section string, b config.AgentBinding, vars map[string]string) (Grant, []string, []string, health.Findings) {
	var problems, warnings []string
	var fs health.Findings
	// Recorders: append the prose (unchanged) AND the classified finding, so the
	// two can never drift apart.
	bindingProblem := func(msg string) {
		problems = append(problems, msg)
		fs = append(fs, health.Error(health.IDAgentsBinding, "Invalid agent binding", msg).
			About(section).WithRemediation("fix the ["+section+"] binding in .satelle/workflows/agents.toml"))
	}
	bindingWarn := func(msg string) {
		warnings = append(warnings, msg)
		fs = append(fs, health.Warn(health.IDAgentsBinding, "Agent binding advisory", msg).About(section))
	}
	ceilingProblem := func(msg string) {
		problems = append(problems, msg)
		fs = append(fs, health.Error(health.IDReviewerUnsafe, "Reviewer ceiling escaped", msg).
			About(section).WithRemediation("restore a read-only ceiling on ["+section+"]"))
	}
	ceilingWarn := func(msg string) {
		warnings = append(warnings, msg)
		fs = append(fs, health.Warn(health.IDReviewerUnsafe, "Reviewer ceiling not expressed", msg).About(section))
	}
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
		IdleTimeout:       b.IdleTimeout,
		BusyTimeout:       b.BusyTimeout,
		InjectsPrinciples: b.InjectsPrinciples(),
		Role:              role,
		Principles:        b.ResolvedPrinciples(),
		RoleInferred:      config.RoleInferred(b),
		ContextChannel:    config.GrantsContextChannel(b.Tools),
	}
	// idle_timeout is what actually bounds a dispatch; an explicit hard timeout
	// smaller than it would cut a progressing agent before the stall detector
	// ever gets a say — advisory only, the operator may want exactly that
	// ceiling (sty_752c4ef2).
	if hard, herr := b.TimeoutDuration(0); herr == nil && hard > 0 {
		if idle, ierr := b.IdleTimeoutDuration(0); ierr == nil && idle > 0 && idle > hard {
			bindingWarn(fmt.Sprintf(
				"agents.toml [%s] idle_timeout %s exceeds timeout %s — the hard ceiling fires first and the stall detector never gets a chance",
				section, b.IdleTimeout, b.Timeout))
		}
		if busy, berr := b.BusyTimeoutDuration(0); berr == nil && busy > 0 && busy > hard {
			bindingWarn(fmt.Sprintf(
				"agents.toml [%s] busy_timeout %s exceeds timeout %s — the hard ceiling fires first",
				section, b.BusyTimeout, b.Timeout))
		}
	}
	if g.RoleInferred {
		bindingWarn(fmt.Sprintf(
			"agents.toml [%s] has no role= declared — inferred role=%s; set role = %q to make the contract explicit",
			section, role, role))
	}
	// Role/path mismatch warnings (not hard fails — user may reassign intentionally).
	if section == "reviewer" && role != config.RoleReviewer {
		ceilingWarn(fmt.Sprintf(
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
			ceilingWarn(fmt.Sprintf(
				"agents.toml [%s] is role=reviewer with command=in-loop — cannot produce an isolated verdict; gates will refuse",
				section))
		}
		// The reviewer SHELL-GRANT advisory is NOT decided here. Its claim is that
		// the grant is never exercised, and that is not knowable from one binding:
		// a reviewer skill the workflow reaches may shell `satelle` itself, which
		// exercises the grant directly (sty_338a53f8). validate raises it after the
		// workflow loop, where the reachable skill set is known.
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
	if iface != config.InterfaceCommand && iface != config.InterfaceACP && iface != config.InterfaceStream {
		bindingProblem(fmt.Sprintf(
			"agents.toml [%s] interface %q: want %q, %q, or %q",
			section, b.Interface, config.InterfaceCommand, config.InterfaceACP, config.InterfaceStream))
		g.Backend = "invalid"
		return g, problems, warnings, fs
	}

	fields := strings.Fields(cmd)
	lower0 := ""
	if len(fields) > 0 {
		lower0 = strings.ToLower(fields[0])
	}

	// ACP / stream transports: spawn line via RunnerFromBinding; ceiling is
	// tools grant + client permission policy (not argv --deny).
	if iface == config.InterfaceACP || iface == config.InterfaceStream {
		label := "acp"
		if iface == config.InterfaceStream {
			label = "stream"
		}
		runner, err := agentcli.RunnerFromBinding(iface, cmd)
		if err != nil {
			bindingProblem(fmt.Sprintf("agents.toml [%s] %s: %v", section, label, err))
			g.Backend = "invalid"
		} else {
			g.Backend = label + ":" + runner.Name()
			if g.Notes == "" {
				g.Notes = label + " spawn: " + runner.Command()
			} else {
				g.Notes += "; " + label + " spawn: " + runner.Command()
			}
			// Ceiling: tools grant + client permission policy (not argv --deny).
			g.ReadOnly = b.Tools != "" && !toolsGrantMutators(b.Tools)
			if role == config.RoleReviewer {
				if b.Tools == "" {
					ceilingProblem(fmt.Sprintf(
						"agents.toml [%s] interface=%s role=reviewer requires tools= (grant evidence; %s ceiling is tools + client permission policy, not argv --deny)",
						section, label, label))
				} else if !g.ReadOnly {
					ceilingWarn(fmt.Sprintf(
						"agents.toml [%s] is role=reviewer with interface=%s and tools that appear to allow mutators (%s) — prefer a read-only tools list; satelle denies mutator tool kinds only when tools look read-only",
						section, label, b.Tools))
				} else if g.Notes == "" {
					g.Notes = "ceiling: " + label + " permission policy + tools"
				} else {
					g.Notes += "; ceiling: " + label + " permission policy + tools"
				}
			}
		}
		if _, err := b.TimeoutDuration(0); err != nil {
			bindingProblem(fmt.Sprintf("agents.toml [%s] timeout: %v", section, err))
		}
		return g, problems, warnings, fs
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
		bindingProblem(fmt.Sprintf(
			"agents.toml [%s] command %q: bare CLI presets removed — use a full command template or run satelle init to migrate",
			section, fields[0]))
	default:
		runner, err := agentcli.RunnerFromCommand(cmd)
		if err != nil {
			bindingProblem(fmt.Sprintf("agents.toml [%s] command: %v", section, err))
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
			bindingProblem(fmt.Sprintf(
				"agents.toml [%s] command omits {system} as its own argv token — the gate/skill rubric is never appended and the agent runs without its rubric",
				section))
		}
		// Hard-reject danger sandbox for role=reviewer (sty_3b4909bb AC3) — not a
		// warning. danger-full-access / --dangerously-bypass-* erase the ceiling.
		codexCmd := isCodexCommand(name, resolved)
		dangerHard := role == config.RoleReviewer && commandHasDangerSandbox(resolved)
		if dangerHard {
			ceilingProblem(fmt.Sprintf(
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
			ceilingProblem(fmt.Sprintf(
				"agents.toml [%s] is role=reviewer with a Codex command whose effective sandbox is %q (want -s read-only) — refuse; use DefaultCodexExecCommand",
				section, effectiveCodexSandbox(resolved)))
		}
		// Reviewer read-only ceiling: advisory when role=reviewer but no ceiling
		// is expressed (no --disallowedTools/--deny / read-only heuristic miss).
		// Warn not fail for non-Codex templates — g.ReadOnly is a heuristic.
		// Codex non-RO already produced a Problem above (sty_aa726901).
		if role == config.RoleReviewer && !g.ReadOnly && !dangerHard && !codexSandboxHard {
			ceilingWarn(fmt.Sprintf(
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
		bindingProblem(fmt.Sprintf("agents.toml [%s] timeout: %v", section, err))
	}
	return g, problems, warnings, fs
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

// wfEntry is a workflow the allocation checks read: an authored DOT doc, or one
// category of a DERIVED route presented as if it were one. Both answer the same
// two questions — what is its name, and what is its lifecycle — which is all the
// allocation loop needs (sty_9835070d).
type wfEntry struct {
	docindex.Doc
	route *wfdot.Spec // non-nil for a derived-route category
	// hooksOnly marks the entry that exists to carry a derived route's
	// lifecycle-hook frontmatter (done.toml). It has no lifecycle of its own.
	hooksOnly bool
	// advisors are the agents this category's route declares for the orchestrator
	// to consult — done.toml's park `advisor` and a step's `advise` (sty_338a53f8).
	// They are deliberately absent from the Spec (Spec is topology), so an
	// allocation check reading only the Spec reported every advisor-only binding
	// orphaned.
	advisors []wfroute.Advisor
	// reworks are the bounded consultation loops this category's steps declare.
	// Off the Spec for the same reason advisors are, and a USAGE signal in the
	// same way: a step's rework consult allocates that binding (sty_8e0b29a0).
	reworks []wfroute.Rework
}

func (e wfEntry) spec() (wfdot.Spec, bool) {
	if e.hooksOnly {
		return wfdot.Spec{}, false
	}
	if e.route != nil {
		return *e.route, true
	}
	return wfdot.Spec{}, false // no DOT front end: only a derived route carries a lifecycle
}

// expandRouteSources turns the indexed workflow set into the entries to check.
// A route source contributes one entry per category it declares (named
// `done.toml+step.toml (<category>)`, so a problem says WHICH route it is in) and
// the two halves themselves contribute none — they carry no lifecycle. Every
// other workflow passes through unchanged.
//
// One entry leads: a hooks-only entry carrying done.toml's body. A lifecycle hook
// is frontmatter, and a route declares its hooks ONCE on done.toml for the whole
// route — reporting them per category would list the same create gate five
// times. It has no Spec, so the allocation loop skips straight past its states.
func expandRouteSources(workflows []docindex.Doc) []wfEntry {
	var out []wfEntry
	for _, w := range workflows {
		if wfgovern.IsRouteSource(w.Name) {
			continue
		}
		out = append(out, wfEntry{Doc: w})
	}
	rs := wfgovern.RouteSourceOf(workflows)
	if !rs.Present() {
		return out
	}
	out = append(out, wfEntry{Doc: docindex.Doc{
		Kind: "workflows",
		Name: wfgovern.DerivedRouteName,
		Body: rs.Done,
	}, hooksOnly: true})
	lists, err := wfdot.ParseDone(rs.Done)
	if err != nil {
		return out // structure validate owns an unparseable route source
	}
	for _, l := range lists {
		if _, governs := wfgovern.RouteGoverns(workflows, l.Category); !governs {
			// An authored workflow outranks the shipped route for this category, so
			// the route is not this repo's lifecycle there and has no allocation to
			// check (sty_3795e7f6).
			continue
		}
		// No tags: the tag-scoped augmentations are validated by their own gate
		// declarations, and a tagless build is the route every story shares.
		//
		// RouteSpecFor is the SINGLE derivation chain (sty_a989764d): it hands back
		// the Spec and the advisors off one parse. Deriving them here instead would
		// mean re-deriving the advisors, and the only correct source for those is
		// the SELECTED steps — a Catalogue.Steps walk hands one route family's
		// advisor to every route with a step of that name (sty_a7316b06).
		dr, err := wfgovern.RouteSpecFor(rs, l.Category, nil)
		if err != nil {
			continue
		}
		doc := docindex.Doc{
			Kind: "workflows",
			Name: wfgovern.DerivedRouteName + " (" + l.Category + ")",
			Body: rs.Step,
		}
		s := dr.Spec
		out = append(out, wfEntry{Doc: doc, route: &s, advisors: dr.Advisors, reworks: dr.Reworks})
	}
	return out
}
