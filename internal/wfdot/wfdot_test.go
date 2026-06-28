package wfdot

import "testing"

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
