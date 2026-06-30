package web

import (
	"strings"
	"testing"
)

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

// TestParseStateAgentAlias proves parseState accepts the canonical `agent:`
// inline key as well as the legacy `actor:`, with agent winning when both are
// present (sty_536f9960).
func TestParseStateAgentAlias(t *testing.T) {
	if got := parseState("{name: in_progress, agent: executor}").Agent; got != "executor" {
		t.Errorf("agent: spelling = %q, want executor", got)
	}
	if got := parseState("{name: gate, actor: reviewer}").Agent; got != "reviewer" {
		t.Errorf("legacy actor: spelling = %q, want reviewer", got)
	}
	if got := parseState("{name: gate, agent: reviewer, actor: executor}").Agent; got != "reviewer" {
		t.Errorf("agent should win over actor, got %q", got)
	}
}

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
	if byName["in_progress"].Agent != "executor" {
		t.Errorf("in_progress actor = %q, want executor", byName["in_progress"].Agent)
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

const sampleWorkflowDOT = `---
name: satelle-project-workflow
applies_to: ["*"]
---
# Project workflow (DOT)

` + "```dot" + `
digraph satelle_workflow {
  graph [goal="Drive a story to done", vars="story, repo_root"]
  rankdir=LR
  start [shape=Mdiamond]
  done  [shape=Msquare]

  in_progress   [actor=executor]
  commit_push   [actor=executor, prompt="@skill:commit-push"]
  commit_review [actor=reviewer, prompt="@skill:satelle-commit-push-reviewer"]
  done_review   [actor=reviewer, prompt="@skill:satelle-story-done-review"]

  start -> in_progress -> commit_push -> commit_review -> done_review -> done
}
` + "```" + `
`

func TestParseWorkflowDOT(t *testing.T) {
	spec := parseWorkflow(sampleWorkflowDOT)
	if len(spec.States) != 6 {
		t.Fatalf("states = %d, want 6: %+v", len(spec.States), spec.States)
	}
	byName := map[string]wfState{}
	for _, s := range spec.States {
		byName[s.Name] = s
	}
	if byName["commit_push"].Agent != "executor" {
		t.Errorf("commit_push actor = %q, want executor", byName["commit_push"].Agent)
	}
	if byName["commit_review"].Agent != "reviewer" {
		t.Errorf("commit_review actor = %q, want reviewer", byName["commit_review"].Agent)
	}
	if !byName["done"].Terminal {
		t.Errorf("done should be terminal")
	}
	if byName["start"].Terminal {
		t.Errorf("start should not be terminal")
	}
	// A transition INTO a reviewer node is gated by that node's skill; edges into
	// executor / plain nodes are ungated.
	skillByTarget := map[string]string{}
	for _, tr := range spec.Transitions {
		skillByTarget[tr.To] = tr.Skill
	}
	if got := skillByTarget["commit_review"]; got != "satelle-commit-push-reviewer" {
		t.Errorf("edge into commit_review skill = %q, want satelle-commit-push-reviewer", got)
	}
	if got := skillByTarget["done_review"]; got != "satelle-story-done-review" {
		t.Errorf("edge into done_review skill = %q, want satelle-story-done-review", got)
	}
	if got := skillByTarget["commit_push"]; got != "" {
		t.Errorf("edge into executor commit_push should be ungated, got %q", got)
	}
	if got := skillByTarget["done"]; got != "" {
		t.Errorf("edge into terminal done should be ungated, got %q", got)
	}
}

func TestWorkflowDiagramFromDOT(t *testing.T) {
	spec := parseWorkflow(sampleWorkflowDOT)
	html := string(workflowDiagram(spec))
	for _, want := range []string{"<svg", "commit_push", "commit_review", "<path", "satelle-commit-push-reviewer"} {
		if !strings.Contains(html, want) {
			t.Errorf("diagram HTML missing %q", want)
		}
	}
}
