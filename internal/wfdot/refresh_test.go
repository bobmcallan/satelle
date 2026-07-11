package wfdot

import (
	"strings"
	"testing"
)

func TestRefresh_LegacyEdgesAndPrompts(t *testing.T) {
	body := `---
name: server-like
---
# Server-like lag

` + "```dot" + `
digraph w {
  rankdir=LR
  graph [goal="ship", vars="story"]
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  integration [agent=executor]
  done        [shape=Msquare]
  // recovery
  integration -> in_progress
  backlog -> in_progress [reviewer_skill="satelle-story-intent-review"]
  in_progress -> integration [reviewer_skill="satelle-code-ac-review"]
  integration -> done
}
` + "```" + `

## Environment

` + "```yaml" + `
guardrails:
  always:
    - keep me
` + "```" + `
`
	out, changed, report := Refresh(body, map[string]string{
		"in_progress": "code",
		"integration": "integrate",
	})
	if !changed {
		t.Fatal("expected changes on legacy fixture")
	}
	if strings.Contains(out, "reviewer_skill=") {
		t.Errorf("legacy reviewer_skill= must be rewritten:\n%s", out)
	}
	if !strings.Contains(out, `agent=reviewer, prompt="@skill:satelle-story-intent-review"`) {
		t.Errorf("node-consistent intent gate missing:\n%s", out)
	}
	if !strings.Contains(out, `prompt="@skill:code"`) || !strings.Contains(out, `prompt="@skill:integrate"`) {
		t.Errorf("performing prompts missing:\n%s", out)
	}
	// Topology / prose preserved
	if !strings.Contains(out, "integration -> in_progress") {
		t.Error("recovery edge must be preserved")
	}
	if !strings.Contains(out, "keep me") || !strings.Contains(out, "guardrails:") {
		t.Error("guardrails prose must be preserved")
	}
	if !strings.Contains(out, "// recovery") {
		t.Error("DOT comments must be preserved")
	}
	if len(report.Applied) == 0 {
		t.Error("report should list applied changes")
	}
	// Result has no format drift of the kinds we rewrote
	fs, ok := FormatDrift(out)
	if !ok {
		t.Fatal("refreshed body must parse")
	}
	for _, f := range fs {
		if f.Kind == "legacy_edge_gate" || f.Kind == "promptless_performing" {
			t.Errorf("refreshed body still has %s @ %s: %s", f.Kind, f.Where, f.Detail)
		}
	}
}

func TestRefresh_IdempotentOnCanonical(t *testing.T) {
	body := `---
name: canonical
---
` + "```dot" + `
digraph w {
  graph [goal="g", vars="v"]
  rankdir=LR
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done [shape=Msquare]
  backlog -> in_progress [agent=reviewer, prompt="@skill:intent"]
  in_progress -> done
}
` + "```" + `
`
	out, changed, report := Refresh(body, nil)
	if changed {
		t.Fatalf("canonical body must be no-op, got:\n%s", out)
	}
	if out != body {
		t.Error("idempotent refresh must return identical body")
	}
	if len(report.Applied) != 0 {
		t.Errorf("no applied changes expected: %v", report.Applied)
	}
}

func TestRefresh_PromptGapWithoutMapping(t *testing.T) {
	body := `---
name: gap
---
` + "```dot" + `
digraph w {
  graph [goal="g", vars="v"]
  rankdir=LR
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare]
  backlog -> in_progress [reviewer_skill="intent"]
  in_progress -> done
}
` + "```" + `
`
	out, changed, report := Refresh(body, nil)
	if !changed {
		t.Fatal("legacy edge should still rewrite without prompt map")
	}
	if !strings.Contains(out, `prompt="@skill:intent"`) {
		t.Error("edge rewrite failed")
	}
	// Promptless performing remains (gap)
	if !strings.Contains(out, "[agent=executor]") || strings.Contains(out, `prompt="@skill:code"`) {
		// still promptless — good if no mapping
	}
	if len(report.Gaps) == 0 {
		t.Error("expected gap for promptless in_progress")
	}
}
