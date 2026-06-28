package wfdot

import (
	"strings"
	"testing"
)

const sampleDOT = `---
name: x
---
# w

` + "```dot" + `
digraph w {
  rankdir=LR
  start       [shape=Mdiamond]
  done        [shape=Msquare, actor=reviewer, prompt="@skill:satelle-story-done-review"]
  in_progress [actor=executor]
  committed   [actor=reviewer, prompt="@skill:satelle-commit-push-reviewer"]
  start -> in_progress -> committed -> done
}
` + "```" + `
`

func TestParse(t *testing.T) {
	spec, ok := Parse(sampleDOT)
	if !ok {
		t.Fatal("expected ok=true for a body with a dot block")
	}
	if len(spec.States) != 4 {
		t.Fatalf("states = %d, want 4: %+v", len(spec.States), spec.States)
	}
	byName := map[string]State{}
	for _, s := range spec.States {
		byName[s.Name] = s
	}
	if byName["in_progress"].Actor != "executor" {
		t.Errorf("in_progress actor = %q, want executor", byName["in_progress"].Actor)
	}
	if byName["committed"].Actor != "reviewer" {
		t.Errorf("committed actor = %q, want reviewer", byName["committed"].Actor)
	}
	if !byName["done"].Terminal {
		t.Errorf("done should be terminal")
	}
	if byName["start"].Terminal {
		t.Errorf("start should not be terminal")
	}
	skill := map[string]string{}
	edge := map[string]bool{}
	for _, tr := range spec.Transitions {
		skill[tr.To] = tr.Skill
		edge[tr.From+"->"+tr.To] = true
	}
	if got := skill["committed"]; got != "satelle-commit-push-reviewer" {
		t.Errorf("entry to committed gate = %q, want satelle-commit-push-reviewer", got)
	}
	if got := skill["done"]; got != "satelle-story-done-review" {
		t.Errorf("entry to done gate = %q, want satelle-story-done-review", got)
	}
	if got := skill["in_progress"]; got != "" {
		t.Errorf("entry to executor in_progress should be ungated, got %q", got)
	}
	if !edge["in_progress->committed"] {
		t.Errorf("missing edge in_progress->committed: %+v", spec.Transitions)
	}
}

func TestParseNoBlock(t *testing.T) {
	if _, ok := Parse("no dot block here\n```yaml\nstates: []\n```"); ok {
		t.Error("expected ok=false when the body has no dot block")
	}
}

func hasProblem(ps []string, substr string) bool {
	for _, p := range ps {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}

func TestValidate(t *testing.T) {
	// sampleDOT reaches a reviewer-gated `done` (done-review) — valid.
	spec, _ := Parse(sampleDOT)
	if p := Validate(spec); len(p) != 0 {
		t.Errorf("sampleDOT should validate clean, got %v", p)
	}
	// dangling edge endpoint
	if p := Validate(Spec{States: []State{{Name: "a"}}, Transitions: []Transition{{From: "a", To: "ghost"}}}); !hasProblem(p, "unknown state") {
		t.Errorf("dangling edge not caught: %v", p)
	}
	// no terminal (2-cycle)
	if p := Validate(Spec{States: []State{{Name: "a"}, {Name: "b"}}, Transitions: []Transition{{From: "a", To: "b"}, {From: "b", To: "a"}}}); !hasProblem(p, "no terminal") {
		t.Errorf("no-terminal not caught: %v", p)
	}
	// done must be terminal
	if p := Validate(Spec{States: []State{{Name: "done"}, {Name: "x"}}, Transitions: []Transition{{From: "done", To: "x"}}}); !hasProblem(p, "must be terminal") {
		t.Errorf("done-not-terminal not caught: %v", p)
	}
	// missing mandatory spine gate on the edge into done
	if p := Validate(Spec{States: []State{{Name: "a"}, {Name: "done"}}, Transitions: []Transition{{From: "a", To: "done"}}}); !hasProblem(p, "spine gate") {
		t.Errorf("missing spine gate not caught: %v", p)
	}
	// spine gate present → valid
	if p := Validate(Spec{States: []State{{Name: "a"}, {Name: "done"}}, Transitions: []Transition{{From: "a", To: "done", Skill: RequiredDoneGate}}}); len(p) != 0 {
		t.Errorf("valid spine should pass, got %v", p)
	}
	// no states
	if p := Validate(Spec{}); !hasProblem(p, "no states") {
		t.Errorf("empty spec not caught: %v", p)
	}
}

func TestEdgeLevelGate(t *testing.T) {
	body := `---
name: b
---
` + "```dot" + `
digraph b {
  backlog     [shape=Mdiamond]
  in_progress [actor=executor]
  done        [shape=Msquare, actor=reviewer, prompt="@skill:satelle-story-done-review"]
  backlog -> in_progress [reviewer_skill="satelle-story-intent-review"]
  in_progress -> done
}
` + "```" + `
`
	spec, ok := Parse(body)
	if !ok {
		t.Fatal("expected ok")
	}
	skill := map[string]string{}
	for _, tr := range spec.Transitions {
		skill[tr.From+"->"+tr.To] = tr.Skill
	}
	// Edge-level reviewer_skill gates an edge into an EXECUTOR node (the intent gate).
	if got := skill["backlog->in_progress"]; got != "satelle-story-intent-review" {
		t.Errorf("edge-level gate = %q, want satelle-story-intent-review", got)
	}
	// Node-derived gate still works for a reviewer target.
	if got := skill["in_progress->done"]; got != "satelle-story-done-review" {
		t.Errorf("node gate = %q, want satelle-story-done-review", got)
	}
	if p := Validate(spec); len(p) != 0 {
		t.Errorf("baseline-shaped DOT should validate clean, got %v", p)
	}
}

func TestParseStripsLineComments(t *testing.T) {
	body := `---
name: c
---
` + "```dot" + `
digraph c {
  in_progress [actor=executor]
  committed   [actor=reviewer, prompt="@skill:satelle-commit-push-reviewer"]
  done        [shape=Msquare, actor=reviewer, prompt="@skill:satelle-story-done-review"]
  in_progress -> committed -> done
  committed   -> in_progress  // recovery: a done-review reject returns to work
}
` + "```" + `
`
	spec, ok := Parse(body)
	if !ok {
		t.Fatal("expected ok")
	}
	edges := map[string]bool{}
	for _, tr := range spec.Transitions {
		edges[tr.From+"->"+tr.To] = true
	}
	// The commented recovery edge must parse to the CLEAN target, not "in_progress // ...".
	if !edges["committed->in_progress"] {
		t.Errorf("commented edge committed->in_progress not parsed; transitions=%+v", spec.Transitions)
	}
	// No state name should carry comment text.
	for _, s := range spec.States {
		if strings.Contains(s.Name, "/") || strings.Contains(s.Name, "recovery") {
			t.Errorf("garbled state from comment: %q", s.Name)
		}
	}
}

func TestParsePreservesSlashesInQuotes(t *testing.T) {
	body := `---
name: d
---
` + "```dot" + `
digraph d {
  graph [goal="see https://example.com/docs for details"]
  in_progress [actor=executor]
  done        [shape=Msquare, actor=reviewer, prompt="@skill:satelle-story-done-review"]
  in_progress -> done
}
` + "```" + `
`
	spec, ok := Parse(body)
	if !ok {
		t.Fatal("expected ok")
	}
	// The // inside the quoted goal must NOT split a statement or spawn a state.
	for _, s := range spec.States {
		if strings.Contains(s.Name, "http") || strings.Contains(s.Name, "example") {
			t.Errorf("quoted URL leaked into a state name: %q", s.Name)
		}
	}
	edges := map[string]bool{}
	for _, tr := range spec.Transitions {
		edges[tr.From+"->"+tr.To] = true
	}
	if !edges["in_progress->done"] {
		t.Errorf("edge in_progress->done not parsed with a quoted URL present; transitions=%+v", spec.Transitions)
	}
}

func TestToDOT(t *testing.T) {
	yamlWF := `---
name: satelle-x-workflow
---
# X

` + "```yaml" + `
states:
  - backlog
  - {name: in_progress, actor: executor}
  - done
transitions:
  - {from: backlog, to: in_progress, reviewer_skill: "satelle-story-intent-review"}
  - {from: in_progress, to: done, reviewer_skill: "satelle-story-done-review"}
` + "```" + `

## Environment

` + "```yaml" + `
guardrails:
  always:
    - keep it
` + "```" + `
`
	out, changed := ToDOT(yamlWF)
	if !changed {
		t.Fatal("expected YAML to convert")
	}
	if dotBlock(out) == "" {
		t.Fatal("converted body has no dot block")
	}
	if !strings.Contains(out, "guardrails:") || !strings.Contains(out, "keep it") {
		t.Error("the guardrails YAML block must be preserved")
	}
	// Round-trip: the converted DOT parses to the same gated lifecycle.
	spec, ok := Parse(out)
	if !ok {
		t.Fatal("converted body should parse as DOT")
	}
	skill := map[string]string{}
	for _, tr := range spec.Transitions {
		skill[tr.From+"->"+tr.To] = tr.Skill
	}
	if skill["backlog->in_progress"] != "satelle-story-intent-review" {
		t.Errorf("intent gate lost in conversion: %v", skill)
	}
	if skill["in_progress->done"] != "satelle-story-done-review" {
		t.Errorf("done gate lost in conversion: %v", skill)
	}
	if p := Validate(spec); len(p) != 0 {
		t.Errorf("converted workflow should validate clean: %v", p)
	}
	// Idempotent: a DOT body is returned unchanged.
	if _, changed2 := ToDOT(out); changed2 {
		t.Error("ToDOT must be idempotent on a DOT body")
	}
}
