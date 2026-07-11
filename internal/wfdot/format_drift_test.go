package wfdot

import (
	"strings"
	"testing"
)

func TestFormatDrift_LegacyServerShape(t *testing.T) {
	// Mirrors the satelle-server-workflow lag case: reviewer_skill= edges +
	// prompt-less performing nodes.
	body := `---
name: server-like
---
` + "```dot" + `
digraph w {
  rankdir=LR
  graph [goal="ship", vars="story"]
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  integration [agent=executor]
  done        [shape=Msquare]
  backlog -> in_progress [reviewer_skill="satelle-story-intent-review"]
  in_progress -> integration [reviewer_skill="satelle-code-ac-review"]
  integration -> done
}
` + "```" + `
`
	fs, ok := FormatDrift(body)
	if !ok {
		t.Fatal("expected parseable DOT")
	}
	if len(fs) == 0 {
		t.Fatal("server-like workflow must report format drift")
	}
	kinds := map[string]int{}
	for _, f := range fs {
		kinds[f.Kind]++
		t.Logf("%s @ %s: %s", f.Kind, f.Where, f.Detail)
	}
	if kinds["legacy_edge_gate"] < 2 {
		t.Errorf("want ≥2 legacy_edge_gate, got %d", kinds["legacy_edge_gate"])
	}
	if kinds["promptless_performing"] < 2 {
		t.Errorf("want ≥2 promptless_performing, got %d", kinds["promptless_performing"])
	}
}

func TestFormatDrift_CanonicalProjectShape(t *testing.T) {
	// Mirrors satelle-project-workflow: node-consistent edges + performing prompts.
	body := `---
name: project-like
---
` + "```dot" + `
digraph w {
  graph [goal="Drive a story to done", vars="story, repo_root"]
  rankdir=LR
  backlog     [shape=Mdiamond]
  plan        [agent=planner, prompt="@skill:plan"]
  in_progress [agent=executor, prompt="@skill:code"]
  done        [shape=Msquare]
  // recovery edge — repo-specific topology, NOT format drift
  integration [agent=executor, prompt="@skill:integrate"]
  backlog -> plan [agent=reviewer, prompt="@skill:satelle-story-intent-review"]
  plan -> in_progress [agent=reviewer, prompt="@skill:satelle-story-plan-review"]
  in_progress -> integration [agent=reviewer, prompt="@skill:satelle-code-ac-review"]
  integration -> in_progress
  integration -> done [agent=reviewer, prompt="@skill:satelle-story-done-review"]
}
` + "```" + `
`
	fs, ok := FormatDrift(body)
	if !ok {
		t.Fatal("expected parseable DOT")
	}
	for _, f := range fs {
		if f.Kind == "legacy_edge_gate" || f.Kind == "promptless_performing" {
			t.Errorf("canonical project shape must not report %s @ %s: %s", f.Kind, f.Where, f.Detail)
		}
	}
}

func TestFormatDrift_NoFalsePositiveOnTopology(t *testing.T) {
	// Extra states (deploy), recovery edges, on= reviewers — not format drift.
	body := `---
name: rich
---
` + "```dot" + `
digraph w {
  graph [goal="g", vars="v"]
  rankdir=LR
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  deploy [agent=executor, prompt="@skill:deploy"]
  done [shape=Msquare]
  estimate [agent=reviewer, prompt="@skill:estimate", on="in_progress,done"]
  backlog -> in_progress [agent=reviewer, prompt="@skill:intent"]
  in_progress -> deploy [agent=reviewer, prompt="@skill:code-ac"]
  deploy -> done [agent=reviewer, prompt="@skill:done"]
  deploy -> in_progress
}
` + "```" + `
`
	fs, ok := FormatDrift(body)
	if !ok {
		t.Fatal("expected parseable DOT")
	}
	if len(fs) != 0 {
		var b strings.Builder
		for _, f := range fs {
			b.WriteString(f.Kind + "@" + f.Where + "; ")
		}
		t.Errorf("repo-specific topology must not be format drift: %s", b.String())
	}
}

func TestFormatDrift_MissingGraphAttrs(t *testing.T) {
	body := `---
name: bare
---
` + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  done [shape=Msquare]
  backlog -> done
}
` + "```" + `
`
	fs, ok := FormatDrift(body)
	if !ok {
		t.Fatal("expected parseable DOT")
	}
	n := 0
	for _, f := range fs {
		if f.Kind == "missing_graph_attr" {
			n++
		}
	}
	if n < 2 {
		t.Errorf("want missing graph attrs, got %v", fs)
	}
}
