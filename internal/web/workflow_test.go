package web

import (
	"fmt"
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

// TestWorkflowDiagramNodeConsistentEdgeGate: the web DAG parser reads an edge
// gate declared in the node-consistent form (agent=reviewer, prompt="@skill:…")
// and the rendered diagram labels that edge with the gate (sty_be67919a).
func TestWorkflowDiagramNodeConsistentEdgeGate(t *testing.T) {
	dot := "---\nname: nc\n---\n" + "```dot" + `
digraph nc {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  done        [shape=Msquare]
  backlog -> in_progress [agent=reviewer, prompt="@skill:satelle-story-intent-review"]
  in_progress -> done
}
` + "```\n"
	spec := parseWorkflow(dot)
	skillByTarget := map[string]string{}
	for _, tr := range spec.Transitions {
		skillByTarget[tr.To] = tr.Skill
	}
	if got := skillByTarget["in_progress"]; got != "satelle-story-intent-review" {
		t.Errorf("node-consistent edge gate = %q, want satelle-story-intent-review", got)
	}
	html := string(workflowDiagram(spec))
	if !strings.Contains(html, "satelle-story-intent-review") {
		t.Errorf("diagram did not label the node-consistent edge gate:\n%s", html)
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

// layeredDOT mirrors the project workflow's shape: a gated spine, a recovery
// (back) edge, a cancel fan, scoped (on=…) reviewers, and an edge-less
// step-summary declaration — the fixture for the layered layout (sty_677c604c).
const layeredDOT = `---
name: w
applies_to: ["*"]
---
` + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  integration [agent=integrator, prompt="@skill:integrate"]
  commit      [agent=commit-agent, prompt="@skill:commit"]
  done        [shape=Msquare]
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  step        [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]
  estimate    [agent=reviewer, prompt="@skill:satelle-estimate-actual-review", on="in_progress,done"]
  backlog -> in_progress
  in_progress -> integration [reviewer_skill="satelle-code-ac-review"]
  integration -> commit [reviewer_skill="satelle-integration-review"]
  commit -> done
  commit -> in_progress
  backlog -> cancelled
  in_progress -> cancelled
  integration -> cancelled
}
` + "```" + `
`

// nodePos extracts each wf-dnode's rect x/y keyed by data-state.
func nodePos(t *testing.T, html string) map[string][2]int {
	t.Helper()
	re := regexp.MustCompile(`<g class="wf-dnode[^"]*" data-state="([^"]+)"[^>]*><rect x="(\d+)" y="(\d+)"`)
	out := map[string][2]int{}
	for _, m := range re.FindAllStringSubmatch(html, -1) {
		var x, y int
		fmt.Sscanf(m[2], "%d", &x)
		fmt.Sscanf(m[3], "%d", &y)
		out[m[1]] = [2]int{x, y}
	}
	return out
}

// TestWorkflowDiagramLayeredSpine covers sty_677c604c AC1: the main spine reads
// left-to-right on ONE row (strictly increasing x, equal y), instead of the old
// vertical stack with every edge arcing through the same right-hand field.
func TestWorkflowDiagramLayeredSpine(t *testing.T) {
	html := string(workflowDiagram(parseWorkflow(layeredDOT)))
	pos := nodePos(t, html)
	spine := []string{"backlog", "in_progress", "integration", "commit", "done"}
	for i := 1; i < len(spine); i++ {
		prev, cur := pos[spine[i-1]], pos[spine[i]]
		if cur[0] <= prev[0] {
			t.Errorf("spine x must increase: %s(x=%d) !> %s(x=%d)", spine[i], cur[0], spine[i-1], prev[0])
		}
		if cur[1] != prev[1] {
			t.Errorf("spine must share one row: %s(y=%d) != %s(y=%d)", spine[i], cur[1], spine[i-1], prev[1])
		}
	}
	// A gate label anchors BETWEEN its edge's nodes.
	re := regexp.MustCompile(`<text class="wf-edge-label" data-from="in_progress" data-to="integration" x="(\d+)"`)
	m := re.FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("code-ac gate label missing:\n%s", html)
	}
	var lx int
	fmt.Sscanf(m[1], "%d", &lx)
	if lx <= pos["in_progress"][0] || lx >= pos["integration"][0]+148 {
		t.Errorf("gate label x=%d not anchored between its nodes (%d..%d)", lx, pos["in_progress"][0], pos["integration"][0])
	}
	// The diagram keeps its original viewBox for JS pan/zoom reset.
	if !strings.Contains(html, `data-vb="0 0 `) {
		t.Error("diagram missing data-vb (pan/zoom reset box)")
	}
}

// TestWorkflowDiagramAltAndAnnotations covers sty_677c604c AC2: cancel-fan and
// recovery edges are de-emphasised (wf-edge-alt, the JS toggle's target), and a
// scoped (on=…) reviewer renders as an annotation on each target state — never
// as a free-floating node — while an edge-less unscoped declaration becomes a
// footnote.
func TestWorkflowDiagramAltAndAnnotations(t *testing.T) {
	html := string(workflowDiagram(parseWorkflow(layeredDOT)))
	// Recovery commit→in_progress is a BACK edge: muted + toggleable.
	if !regexp.MustCompile(`<path class="wf-edge-path[^"]*back wf-edge-alt" data-from="commit" data-to="in_progress"`).MatchString(html) {
		t.Errorf("recovery edge should carry back + wf-edge-alt:\n%s", html)
	}
	// Every cancel edge is de-emphasised.
	for _, from := range []string{"backlog", "in_progress", "integration"} {
		re := regexp.MustCompile(`<path class="wf-edge-path[^"]*wf-edge-alt" data-from="` + from + `" data-to="cancelled"`)
		if !re.MatchString(html) {
			t.Errorf("cancel edge %s→cancelled should carry wf-edge-alt:\n%s", from, html)
		}
	}
	// estimate (on="in_progress,done") annotates BOTH targets and is NOT a node.
	for _, target := range []string{"in_progress", "done"} {
		if !strings.Contains(html, `<text class="wf-annot" data-state="`+target+`"`) {
			t.Errorf("scoped reviewer should annotate %s:\n%s", target, html)
		}
	}
	if strings.Contains(html, `data-state="estimate"`) && strings.Contains(html, `<g class="wf-dnode" data-state="estimate"`) {
		t.Error("a scoped reviewer must not render as a free-floating node")
	}
	// step (edge-less, no on=) becomes the footnote line.
	if !strings.Contains(html, `class="wf-foot"`) || !strings.Contains(html, "step-summary") {
		t.Errorf("edge-less step declaration should render as a footnote:\n%s", html)
	}
	// A named-agent state carries its @agent badge.
	if !strings.Contains(html, `class="wf-agent-badge"`) || !strings.Contains(html, "@integrator") {
		t.Errorf("named-agent state should carry its badge:\n%s", html)
	}
}

// TestWorkflowDiagramExecutorAugmentationAnnotation (sty_8225d8a5 AC6): an
// edge-less executor with on= is an annotation on the target spine state, never
// a free-floating lifecycle node (the phantom that agent==reviewer-only split
// would reintroduce).
func TestWorkflowDiagramExecutorAugmentationAnnotation(t *testing.T) {
	dot := `---
name: w
applies_to: ["*"]
---
` + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done [shape=Msquare]
  codeui [agent=executor, prompt="@skill:code-ui", on="in_progress", applies_to="surface:ui"]
  backlog -> in_progress -> done
}
` + "```" + "\n"
	html := string(workflowDiagram(parseWorkflow(dot)))
	if strings.Contains(html, `<g class="wf-dnode" data-state="codeui"`) {
		t.Errorf("augmentation must not be a main-flow node:\n%s", html)
	}
	if !strings.Contains(html, `class="wf-annot" data-state="in_progress"`) {
		t.Errorf("augmentation should annotate in_progress:\n%s", html)
	}
	// Spine nodes still present.
	if !strings.Contains(html, `data-state="in_progress"`) {
		t.Error("spine in_progress missing")
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
	re := regexp.MustCompile(`<text class="wf-edge-label[^"]*"[^>]*\bx="(\d+)" y="(\d+)"`)
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
