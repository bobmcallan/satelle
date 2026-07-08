//go:build integration

package tests

import (
	"os"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// TestProjectWorkflowReviewerFirst asserts this repo's project workflow has the
// reviewer-first, fully-dispatched shape (sty_fd9d210f, extended by the
// all-sonnet consolidation sty_8fa35ca1): a reviewer gates every transition, and
// EVERY performing stage is dispatched to an isolated named agent on sonnet —
// plan (the read-only planner), and in_progress + integration + release (all the
// code-writer worker). The driving session orchestrates transitions and lets the
// gates judge; it performs no step itself. backlog -> plan is gated by the intake
// reviewer satelle-story-intent-review (sty_3437b803). The former
// commit/push/committed states are merged into one `release` state, and there are
// recovery edges back to in_progress (no dead-end). `integration` is an explicit,
// visible testing step (sty_15dbc0dd). plan stays on its own read-only binding
// because it is entered from the non-performing backlog state and the dispatch
// lock-guard refuses a code-writer there (sty_8fa35ca1).
func TestProjectWorkflowReviewerFirst(t *testing.T) {
	body, err := os.ReadFile("../.satelle/workflows/satelle-project-workflow.md")
	if err != nil {
		t.Fatalf("read project workflow: %v", err)
	}
	spec, ok := wfdot.Parse(string(body))
	if !ok {
		t.Fatal("project workflow DOT did not parse")
	}

	states := map[string]wfdot.State{}
	for _, s := range spec.States {
		states[s.Name] = s
	}

	// integration and release are DISPATCHED to the sonnet worker, never in-loop
	// (consolidated off the retired glm performer — sty_8fa35ca1).
	for name, wantSkill := range map[string]string{"integration": "integrate", "release": "release"} {
		s, present := states[name]
		if !present {
			t.Errorf("missing execution state %q", name)
			continue
		}
		if s.Agent != "worker" || s.Skill != wantSkill {
			t.Errorf("state %q must dispatch agent=worker @skill:%s, got agent=%q skill=%q", name, wantSkill, s.Agent, s.Skill)
		}
	}

	// Four steps dispatch: the read-only sonnet planner (plan), the sonnet worker
	// (in_progress), and the same sonnet worker (integration + release, checked above).
	if p, present := states["plan"]; !present {
		t.Error("missing plan state")
	} else if p.Agent != "planner" || p.Skill != "plan" {
		t.Errorf("plan step must dispatch agent=planner @skill:plan, got agent=%q skill=%q", p.Agent, p.Skill)
	}
	if w, present := states["in_progress"]; !present {
		t.Error("missing in_progress state")
	} else if w.Agent != "worker" || w.Skill != "code" {
		t.Errorf("in_progress must dispatch agent=worker @skill:code, got agent=%q skill=%q", w.Agent, w.Skill)
	}

	// The dispatched executor experiment states are gone (merged into release).
	for _, gone := range []string{"commit", "push", "committed"} {
		if _, present := states[gone]; present {
			t.Errorf("state %q should be merged away in the reviewer-only workflow", gone)
		}
	}

	// Edges: the reviewer-gated spine plus the recovery edge.
	type edge struct{ from, to, skill string }
	got := map[edge]bool{}
	hasRecovery := false
	for _, tr := range spec.Transitions {
		got[edge{tr.From, tr.To, tr.Skill}] = true
		if tr.From == "release" && tr.To == "in_progress" {
			hasRecovery = true
		}
	}
	for _, want := range []edge{
		{"backlog", "plan", "satelle-story-intent-review"},
		{"plan", "in_progress", "satelle-story-plan-review"},
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
