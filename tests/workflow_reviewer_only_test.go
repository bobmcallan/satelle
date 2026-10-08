//go:build integration && operatorconfig

package tests

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// TestOperatorProjectWorkflowReviewerFirst asserts this repo's project workflow
// is reviewer-first (it pins the AUTHORED route under .satelle/workflows, which a
// clean checkout does not carry, so it runs under the operatorconfig opt-in —
// make operator-check): a reviewer gates every transition on the spine. Plan
// dispatches to an isolated read-only planner; integration and release run
// IN-LOOP on the driving session (agent=executor). A story has one readiness
// step (sty_5262592e): backlog -> plan, whose planner proposes before the
// intent, plan, architecture and coverage reviewers judge it; plan ->
// in_progress is gated by the definition-unchanged check. The former commit/push/committed
// states are merged into one `release` state, and there are recovery edges back
// to in_progress (no dead-end). `integration` is an explicit, visible testing
// step (sty_15dbc0dd).
//
// in_progress accepts EITHER allocation. sty_db003275 reverted a brief,
// unbounded agent=coder experiment, which is why this used to pin in-loop
// executor@code. sty_ae16cd44 (epic:converge-then-gate order:4) re-opened the
// dispatched coder for this repo, this time WITH a bounded rework relay (coder
// <-> reviewer-consult — consultation, not review), so the pin no longer
// forbids the opt-in: it bounds in_progress to the two sanctioned allocations
// (in-loop executor@code for repos that have not opted in, dispatched
// coder@coder for those that have) and still pins integration and release
// in-loop.
//
// The relay's round budget is deliberately NOT asserted here. rework is an
// orchestrator instruction, not topology, so it is absent from the emitted
// Spec by design; `satelle story route` prints it. Compiling it into a Go
// assertion would be another process-in-Go pin.
func TestOperatorProjectWorkflowReviewerFirst(t *testing.T) {
	spec := repoRouteSpec(t, "*", nil)

	states := map[string]wfdot.State{}
	for _, s := range spec.States {
		states[s.Name] = s
	}

	// integration and release are IN-LOOP (agent=executor).
	for name, wantSkill := range map[string]string{
		"integration": "integrate",
		"release":     "release",
	} {
		s, present := states[name]
		if !present {
			t.Errorf("missing execution state %q", name)
			continue
		}
		if s.Agent != "executor" || s.Skill != wantSkill {
			t.Errorf("state %q must run in-loop agent=executor @skill:%s, got agent=%q skill=%q", name, wantSkill, s.Agent, s.Skill)
		}
	}

	// in_progress is either in-loop or the dispatched coder — nothing else.
	type alloc struct{ agent, skill string }
	allowed := []alloc{{"executor", "code"}, {"coder", "coder"}}
	if s, present := states["in_progress"]; !present {
		t.Errorf("missing execution state %q", "in_progress")
	} else {
		ok := false
		for _, a := range allowed {
			if s.Agent == a.agent && s.Skill == a.skill {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("state %q must run in-loop agent=executor @skill:code or dispatched agent=coder @skill:coder, got agent=%q skill=%q", "in_progress", s.Agent, s.Skill)
		}
	}

	// plan alone dispatches to the isolated planner.
	if p, present := states["plan"]; !present {
		t.Error("missing plan state")
	} else if p.Agent != "planner" || p.Skill != "plan" {
		t.Errorf("plan step must dispatch agent=planner @skill:plan, got agent=%q skill=%q", p.Agent, p.Skill)
	}

	// A story has ONE readiness step (sty_5262592e): no `ready` state; plan is
	// entered straight from backlog, its planner proposes before the gates, and
	// the definition freezes on entry to in_progress.
	if _, present := states["ready"]; present {
		t.Error("a story route must not hold a separate ready state (sty_5262592e)")
	}
	if p := states["plan"]; !p.Propose || p.RejectBudget <= 0 {
		t.Errorf("plan must propose before its gates with a reject budget, got propose=%v reject_budget=%d", p.Propose, p.RejectBudget)
	}

	// The dispatched executor experiment states are gone (merged into release).
	for _, gone := range []string{"commit", "push", "committed"} {
		if _, present := states[gone]; present {
			t.Errorf("state %q should be merged away in the reviewer-only workflow", gone)
		}
	}

	// Edges: the reviewer-gated spine plus the recovery edge.
	// Multi-reviewer edges list every skill (sty_a7eed214 / sty_b034ca97): mechanical
	// preconditions first, then judgment gates — match any skill in Skills.
	type edge struct{ from, to, skill string }
	got := map[edge]bool{}
	hasRecovery := false
	hasBacklogRecover := false
	for _, tr := range spec.Transitions {
		skills := tr.Skills
		if len(skills) == 0 && tr.Skill != "" {
			skills = []string{tr.Skill}
		}
		for _, sk := range skills {
			got[edge{tr.From, tr.To, sk}] = true
		}
		if tr.From == "release" && tr.To == "in_progress" {
			hasRecovery = true
		}
		if tr.From == "plan" && tr.To == "backlog" {
			hasBacklogRecover = true
		}
	}
	for _, want := range []edge{
		{"backlog", "plan", "satelle-story-intent-review"},
		{"backlog", "plan", "satelle-story-plan-review"},
		{"backlog", "plan", "satelle-story-architecture-review"},
		{"backlog", "plan", "satelle-story-integration-coverage-review"},
		{"plan", "in_progress", "satelle-definition-unchanged-check"},
		{"in_progress", "integration", "satelle-ac-evidence-check"},
		{"in_progress", "integration", "satelle-code-ac-review"},
		{"integration", "release", "satelle-integration-review"},
		{"release", "done", "satelle-story-release-review"},
	} {
		if !got[want] {
			t.Errorf("missing gated edge %s -> %s [%s]", want.from, want.to, want.skill)
		}
	}
	if got[edge{"backlog", "ready", "satelle-story-ready-review"}] {
		t.Error("backlog -> ready must not be gated by the read-only satelle-story-ready-review")
	}
	if !hasBacklogRecover {
		t.Error("missing plan -> backlog recover edge (the way back for a definition edited after readiness)")
	}
	if !hasRecovery {
		t.Error("missing release -> in_progress recovery edge (a reject must have a back-edge)")
	}
}
