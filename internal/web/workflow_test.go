package web

import "testing"

const sampleWorkflow = `---
name: satelle-baseline-workflow
applies_to: ["*"]
---
# Baseline

` + "```yaml" + `
states:
  - backlog
  - {name: in_progress, actor: executor}
  - done
  - cancelled
transitions:
  - {from: backlog, to: in_progress, reviewer_skill: "satelle-story-intent-review"}
  - {from: in_progress, to: done, reviewer_skill: "satelle-story-done-review"}
  - {from: backlog, to: cancelled}
` + "```" + `

## Environment

` + "```yaml" + `
guardrails:
  always:
    - Drive an engaged item to a terminal state.
  never:
    - Self-enact a transition.
` + "```" + `
`

func TestParseWorkflow(t *testing.T) {
	spec := parseWorkflow(sampleWorkflow)
	if len(spec.States) != 4 {
		t.Fatalf("states = %d, want 4 (guardrail items must not be parsed as states): %+v", len(spec.States), spec.States)
	}
	// in_progress carries an actor and is non-terminal; done/cancelled are terminal.
	byName := map[string]wfState{}
	for _, s := range spec.States {
		byName[s.Name] = s
	}
	if byName["in_progress"].Actor != "executor" {
		t.Errorf("in_progress actor = %q, want executor", byName["in_progress"].Actor)
	}
	if !byName["done"].Terminal || !byName["cancelled"].Terminal {
		t.Errorf("done/cancelled should be terminal")
	}
	if byName["backlog"].Terminal {
		t.Errorf("backlog should not be terminal")
	}
	if len(spec.Transitions) != 3 {
		t.Fatalf("transitions = %d, want 3: %+v", len(spec.Transitions), spec.Transitions)
	}
	if spec.Transitions[0].Skill != "satelle-story-intent-review" {
		t.Errorf("first transition skill = %q", spec.Transitions[0].Skill)
	}
	if spec.Transitions[2].Skill != "" {
		t.Errorf("backlog→cancelled should be ungated, got %q", spec.Transitions[2].Skill)
	}
}

func TestFrontmatterListWeb(t *testing.T) {
	got := frontmatterList(sampleWorkflow, "applies_to")
	if len(got) != 1 || got[0] != "*" {
		t.Fatalf("applies_to = %v, want [*]", got)
	}
}
