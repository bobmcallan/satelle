package web

import (
	"regexp"
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
  - {name: in_progress, agent: executor}
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

// TestParseStateAgentKey proves parseState reads the canonical `agent:` inline key
// and that the retired `actor:` key no longer sets a performer (sty_7db2ed7d).
func TestParseStateAgentKey(t *testing.T) {
	if got := parseState("{name: in_progress, agent: executor}").Agent; got != "executor" {
		t.Errorf("agent: spelling = %q, want executor", got)
	}
	if got := parseState("{name: gate, actor: reviewer}").Agent; got != "" {
		t.Errorf("retired actor: key must not set a performer, got %q", got)
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

  in_progress   [agent=executor]
  commit_push   [agent=executor, prompt="@skill:commit-push"]
  commit_review [agent=reviewer, prompt="@skill:satelle-commit-push-reviewer"]
  done_review   [agent=reviewer, prompt="@skill:satelle-story-done-review"]

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

// TestWorkflowDiagramCarriesIdentifiers covers sty_19b2107a AC1/AC2: every node
// carries a stable data-state and is focusable, and every edge path + label carries
// data-from/data-to so JS can correlate a node with its incident edges.
func TestWorkflowDiagramCarriesIdentifiers(t *testing.T) {
	spec := parseWorkflow(sampleWorkflowDOT)
	html := string(workflowDiagram(spec))

	// Nodes: a stable data-state and focusability (tabindex) for keyboard a11y.
	for _, want := range []string{
		`data-state="in_progress"`, `data-state="commit_push"`, `data-state="done"`,
		`tabindex="0"`, `role="button"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("node markup missing %q", want)
		}
	}
	// Edge paths AND labels carry data-from/data-to for the edge into commit_review.
	if !strings.Contains(html, `<path class="wf-edge-path" data-from="commit_push" data-to="commit_review"`) {
		t.Errorf("edge path missing data-from/data-to identifiers:\n%s", html)
	}
	if !strings.Contains(html, `<text class="wf-edge-label" data-from="commit_push" data-to="commit_review"`) {
		t.Errorf("edge label missing data-from/data-to identifiers:\n%s", html)
	}
}

// TestWorkflowDiagramLabelsDoNotOverprint covers sty_19b2107a AC4: when several
// edges target the same node (here all the cancel edges, plus the gated forward
// edges), their labels must not collapse onto the same coordinate. The anti-collision
// pass guarantees no two labels share an x-overlap at the same y.
func TestWorkflowDiagramLabelsDoNotOverprint(t *testing.T) {
	// A workflow with multiple gated edges into the same target (cancelled) — the
	// shape that produced the run-together "code-acstory-cancel" label.
	dot := `---
name: w
applies_to: ["*"]
---
` + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  integration [agent=executor]
  commit      [agent=executor]
  done        [shape=Msquare]
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  rev1        [agent=reviewer, prompt="@skill:code-ac-review"]
  backlog -> in_progress -> rev1 -> integration -> commit -> done
  backlog -> cancelled
  in_progress -> cancelled
  integration -> cancelled
  commit -> cancelled
}
` + "```" + `
`
	spec := parseWorkflow(dot)
	html := string(workflowDiagram(spec))

	// Pull each <text class="wf-edge-label" … x=".." y=".."> and assert no two labels
	// occupy the same (x,y) — the overprint the story reports.
	re := regexp.MustCompile(`<text class="wf-edge-label"[^>]*\bx="(\d+)" y="(\d+)"`)
	seen := map[string]bool{}
	matches := re.FindAllStringSubmatch(html, -1)
	if len(matches) < 2 {
		t.Fatalf("expected several edge labels, got %d:\n%s", len(matches), html)
	}
	for _, m := range matches {
		key := m[1] + "," + m[2]
		if seen[key] {
			t.Errorf("two edge labels overprint at the same coordinate %s:\n%s", key, html)
		}
		seen[key] = true
	}
}
