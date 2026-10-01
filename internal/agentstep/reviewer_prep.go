package agentstep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/structure"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// reviewerPrep is one gate's pre-flight: its rubric, its structure and contract
// checks passed, and the transition payload it judges. It is the ONE place that
// pre-flight lives, shared by the single-gate path (runReviewerWith) and the
// bundled path (runReviewerBundle), so a bundled rubric is refused, degraded and
// decorated exactly as it is when it runs alone (sty_23e10d92).
type reviewerPrep struct {
	skill string
	body  string
	tp    transitionPayload
	// payload is tp marshalled — the stdin of a functional check.
	payload []byte
	// unresolved marks a DECLARED gate whose rubric is not installed: advisory,
	// nothing judges it, and the caller records it as an ungated advance.
	unresolved bool
}

// prepareReviewer resolves skill's rubric, refuses a broken one, and builds the
// payload the gate judges. An absent rubric is not an error — it returns
// unresolved (sty_d59ec6a9).
func (g *Engine) prepareReviewer(ctx context.Context, item workitem.Item, toStatus, skill, gateAgent string, decorate func(*transitionPayload)) (reviewerPrep, error) {
	body, err := g.skillBody(ctx, skill)
	if err != nil {
		if errors.Is(err, docindex.ErrNotFound) {
			// Advisory degradation: the edge DECLARED this gate but its rubric is
			// not installed, so nothing judges the transition and it advances.
			// Fail-open is deliberate (a fresh repo must work before every gate is
			// authored) — but it must not be SILENT, so name the skill that was
			// skipped (sty_d59ec6a9).
			return reviewerPrep{skill: skill, unresolved: true}, nil
		}
		return reviewerPrep{}, err
	}
	// Broken substrate refuses to run (sty_d0d6bb67): a PRESENT reviewer skill
	// that fails its deterministic structure check must not judge the edge. An
	// ABSENT rubric stays advisory by design (fresh repos keep working); an
	// invalid one is a broken definition and refuses, naming the problems.
	if problems := structure.Doc("skills", skill, body, nil); len(problems) > 0 {
		return reviewerPrep{}, fmt.Errorf(
			"gate refused: reviewer skill %q fails structure validation: %s — fix the substrate (`satelle skill validate %s`)",
			skill, strings.Join(problems, "; "), skill)
	}
	// Reviewer skill contract check (design §6.3): the body must specify the
	// verdict contract (at least decision + notes). reasoning is recommended.
	if problems := structure.ReviewerSkillContract(body); len(problems) > 0 {
		return reviewerPrep{}, fmt.Errorf(
			"gate refused: reviewer skill %q does not specify the verdict contract: %s — the skill must document returning JSON {decision, notes} (reasoning recommended)",
			skill, strings.Join(problems, "; "))
	}
	tp := transitionPayload{Story: item, From: item.Status, To: toStatus, ReviewSkill: skill}
	if g.children != nil {
		tp.Children = g.children(ctx, item)
	}
	g.fillPayloadDocs(ctx, item.ID, &tp)
	// Prior verdicts ride ONLY the gate payload (sty_0f5e600c): they are re-review
	// context, so the executor and retrospective payloads deliberately go without —
	// a performer optimising for the last rejection instead of the story is the
	// failure mode that would create.
	g.fillPriorVerdicts(ctx, item.ID, item.Status, toStatus, &tp)
	g.fillDefinitionEdits(ctx, item.ID, &tp)
	// Engagement diff rides the GATE payload only (sty_a125b440): reviewers
	// without a shell need the slice; executors have one. fillDiff never
	// errors — a missing baseline is a marker, not a refused transition.
	g.fillDiff(ctx, item.ID, &tp)
	gateAddrs := []string{"reviewer"}
	if strings.TrimSpace(gateAgent) != "" && gateAgent != "reviewer" {
		gateAddrs = append(gateAddrs, gateAgent)
	}
	g.fillMessages(ctx, item.ID, gateAddrs, &tp)
	g.fillMeasuredActual(ctx, item.ID, &tp)
	// Route drift rides the payload ONLY when it exists, so a repo that names a
	// drift gate has the enumeration without shelling for it, and every other
	// reviewer's payload is byte-for-byte unchanged (sty_6e4f7fd8).
	if d, drifted := g.routeDriftFor(ctx, item); drifted {
		tp.RouteDrift = &d
	}
	if decorate != nil {
		decorate(&tp)
	}
	payload, err := json.Marshal(tp)
	if err != nil {
		return reviewerPrep{}, err
	}
	return reviewerPrep{skill: skill, body: body, tp: tp, payload: payload}, nil
}

// reviewerSeat is the resolved harness an LLM gate runs under: the binding with
// its engine-wide fallbacks filled, the agents.toml section it came from, the
// model and why it was chosen, the runner, and the idle bound. Two gates that
// resolve to the same seat can share one session (sty_23e10d92).
type reviewerSeat struct {
	binding     config.AgentBinding
	section     string
	modelSource string
	runner      agentcli.Runner
	idle        time.Duration
}

// reviewerSeatFor resolves the seat gateAgent's LLM gate runs under, refusing a
// binding that cannot produce an isolated verdict. The error is the refusal a
// gate surfaces; the caller supplies the decision that carries it. skill only
// names the gate in the no-runner refusal.
func (g *Engine) reviewerSeatFor(ctx context.Context, item workitem.Item, toStatus, skill, gateAgent string) (reviewerSeat, error) {
	binding, section, berr := g.gateBinding(gateAgent)
	if berr != nil {
		return reviewerSeat{}, berr
	}
	// Engine-wide caches fill only the default [reviewer] binding. Named
	// bindings must be self-contained in agents.toml (sty_a476a2f8).
	if section == "reviewer" {
		if binding.Tools == "" {
			binding.Tools = g.tools
		}
		if binding.Model == "" {
			binding.Model = g.model
		}
		if len(binding.Env) == 0 {
			binding.Env = g.reviewerEnv
		}
		if binding.Principles == "" && binding.InjectPrinciples == nil {
			if g.injectPrinciples {
				binding.Principles = config.PrinciplesSession
			} else {
				binding.Principles = config.PrinciplesNone
			}
		}
	}
	// Model selection (sty_7069bced): binding.Model already carries the
	// explicit configured model or the engine-wide g.model fallback filled
	// above, so this only descends the ladder (inherited/creator/cli-default)
	// when BOTH are empty — an explicit model behaves exactly as before (AC6).
	// A gate has no step/agent override tier of its own (edges superseded
	// their DOT model= at sty_a476a2f8; this does not reverse that).
	modelResolved, modelSource := g.selectModel(ctx, binding, item.ID, "", "")
	binding.Model = modelResolved
	// Mechanism: a gate needs an isolated verdict. command=in-loop cannot produce
	// one — fail loud at gate time (design §6.4), not by policing tools/model.
	if config.IsInLoopCommand(binding.CommandTemplate()) {
		return reviewerSeat{}, fmt.Errorf(
			"gate refused: reviewer binding %q is command=in-loop and cannot produce an isolated verdict — set [%s] command to an isolated agent CLI (claude|grok or a full template)", section, section)
	}
	// Role must resolve to reviewer for the gate binding (design §4.4 / §8 / sty_a476a2f8).
	if config.ResolvedRole(section, binding) != config.RoleReviewer {
		return reviewerSeat{}, fmt.Errorf(
			"gate refused: binding [%s] has role=%q (want role=reviewer) — a named performer never advances status; allocate a role=\"reviewer\" binding on gated edges",
			section, config.ResolvedRole(section, binding))
	}
	// Default [reviewer] uses the bootstrap runner (g.runner). A named
	// role=reviewer binding must run its OWN harness — leave Runner nil so
	// Invoke builds from the binding (sty_68dafd5f; runner must follow agent=).
	var gateRunner agentcli.Runner
	if section == "reviewer" {
		if g.runner == nil {
			return reviewerSeat{}, fmt.Errorf(
				"reviewer: transition %s→%s is gated by %q but no agent runner is configured", item.Status, toStatus, skill)
		}
		gateRunner = g.runner
	}
	idle, ierr := g.idleTimeoutFor(section, binding)
	if ierr != nil {
		return reviewerSeat{}, fmt.Errorf("reviewer: invalid idle_timeout in .satelle/workflows/agents.toml [%s]: %w", section, ierr)
	}
	return reviewerSeat{binding: binding, section: section, modelSource: modelSource, runner: gateRunner, idle: idle}, nil
}

// stampInvocation copies what one Invoke run measured about itself onto the
// decision it produced — the same fields for a lone gate and a bundled one.
func stampInvocation(d *verb.GateDecision, res InvokeResult, modelSource string) {
	d.ModelSource = modelSource
	d.SystemPromptBytes = res.SystemPromptBytes
	d.PayloadBytes = res.PayloadBytes
	d.ToolIsolation = res.ToolIsolation
}
