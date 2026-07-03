//go:build integration

package tests

import (
	"os"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// TestProjectWorkflowIsReviewerOnly asserts this repo's project workflow has the
// simplified reviewer-only shape (sty_d9a0b573): execution steps run in-loop
// (agent=executor, no isolated dispatch), the only dispatched step is the fable
// planner, the former commit/push/committed states are merged into one `release`
// state, and there is a release→in_progress recovery edge (no dead-end at
// release).
func TestProjectWorkflowIsReviewerOnly(t *testing.T) {
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

	// Execution steps are in-loop (agent=executor), never a named dispatch agent.
	for _, name := range []string{"in_progress", "release"} {
		s, present := states[name]
		if !present {
			t.Errorf("missing execution state %q", name)
			continue
		}
		if s.Agent != "executor" {
			t.Errorf("state %q must be in-loop (agent=executor), got agent=%q", name, s.Agent)
		}
	}

	// The ONLY dispatched executor is the fable planner.
	if p, present := states["plan"]; !present {
		t.Error("missing plan state")
	} else if p.Agent != "planner" || p.Skill != "plan" {
		t.Errorf("plan step must dispatch agent=planner @skill:plan, got agent=%q skill=%q", p.Agent, p.Skill)
	}

	// The dispatched executor experiment states are gone (merged into release).
	for _, gone := range []string{"commit", "push", "committed", "integration"} {
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
		{"backlog", "plan", ""},
		{"plan", "in_progress", "satelle-story-plan-review"},
		{"in_progress", "release", "satelle-code-ac-review"},
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
