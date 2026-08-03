//go:build integration

package tests

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// TestProjectWorkflowReviewerFirst asserts this repo's project workflow is
// reviewer-first: a reviewer gates every transition on the spine. Plan
// dispatches to an isolated read-only planner; in_progress, integration, and
// release run IN-LOOP on the driving session (agent=executor) — sty_db003275
// reverted the brief agent=coder experiment. backlog -> plan is gated by
// satelle-story-intent-review (sty_3437b803). The former commit/push/committed
// states are merged into one `release` state, and there are recovery edges back
// to in_progress (no dead-end). `integration` is an explicit, visible testing
// step (sty_15dbc0dd).
func TestProjectWorkflowReviewerFirst(t *testing.T) {
	spec := repoRouteSpec(t, "*", nil)

	states := map[string]wfdot.State{}
	for _, s := range spec.States {
		states[s.Name] = s
	}

	// in_progress, integration, and release are IN-LOOP (agent=executor).
	for name, wantSkill := range map[string]string{
		"in_progress": "code",
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

	// plan alone dispatches to the isolated planner.
	if p, present := states["plan"]; !present {
		t.Error("missing plan state")
	} else if p.Agent != "planner" || p.Skill != "plan" {
		t.Errorf("plan step must dispatch agent=planner @skill:plan, got agent=%q skill=%q", p.Agent, p.Skill)
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
	}
	for _, want := range []edge{
		{"backlog", "plan", "satelle-story-intent-review"},
		{"plan", "in_progress", "satelle-story-plan-review"},
		{"in_progress", "integration", "satelle-ac-evidence-check"},
		{"in_progress", "integration", "satelle-code-ac-review"},
		{"integration", "release", "satelle-integration-review"},
		{"release", "done", "satelle-story-release-review"},
	} {
		if !got[want] {
			t.Errorf("missing gated edge %s -> %s [%s]", want.from, want.to, want.skill)
		}
	}
	if !hasRecovery {
		t.Error("missing release -> in_progress recovery edge (a reject must have a back-edge)")
	}
}
