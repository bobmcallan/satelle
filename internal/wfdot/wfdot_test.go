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
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  in_progress [agent=executor]
  committed   [agent=reviewer, prompt="@skill:satelle-commit-push-reviewer"]
  start -> in_progress -> committed -> done
}
` + "```" + `
`

// TestActorKeywordIgnored proves the legacy actor= keyword no longer parses
// (sty_7db2ed7d): only agent= sets a node's performer, so a node authored with the
// retired actor= attribute gets no performer.
func TestActorKeywordIgnored(t *testing.T) {
	const dot = `---
name: x
---
` + "```dot" + `
digraph w {
  start       [shape=Mdiamond]
  in_progress [agent=executor]
  legacy      [actor=reviewer]
  done        [shape=Msquare, agent=reviewer]
  start -> in_progress -> legacy -> done
}
` + "```" + `
`
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("expected ok=true")
	}
	byName := map[string]State{}
	for _, s := range spec.States {
		byName[s.Name] = s
	}
	if byName["in_progress"].Agent != "executor" {
		t.Errorf("agent=executor should parse, got %q", byName["in_progress"].Agent)
	}
	if byName["legacy"].Agent != "" {
		t.Errorf("legacy actor= must NOT set a performer, got %q", byName["legacy"].Agent)
	}
}

// TestToDOTEmitsAgent proves the emitter writes the canonical agent= keyword
// (sty_384f0b11): an inline-YAML lifecycle with an executor node re-emits as a DOT
// graph carrying agent=executor, never the retired actor=.
func TestToDOTEmitsAgent(t *testing.T) {
	body := `---
name: y
---
` + "```yaml" + `
states:
  - backlog
  - {name: in_progress, agent: executor}
  - done
transitions:
  - {from: backlog, to: in_progress}
  - {from: in_progress, to: done}
` + "```" + `
`
	out, changed := ToDOT(body)
	if !changed {
		t.Fatal("ToDOT should convert inline-YAML to DOT")
	}
	if !strings.Contains(out, "agent=executor") {
		t.Errorf("emitted DOT should carry agent=executor:\n%s", out)
	}
	if strings.Contains(out, "actor=executor") {
		t.Errorf("emitted DOT must not carry the retired actor= keyword:\n%s", out)
	}
}

// TestNamedAgentIsPerforming proves a node allocated to a NAMED agent (not
// executor/reviewer) is treated as a PERFORMING node (sty_b2222b8a): its @skill is
// collected by ExecutorPathToDoneSkills (so a missing rubric is caught), while a
// reviewer node's is not.
func TestNamedAgentIsPerforming(t *testing.T) {
	const dot = `---
name: x
---
` + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  commit_push [agent=commit-agent, prompt="@skill:commit-push"]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  backlog -> in_progress -> commit_push -> done
}
` + "```" + `
`
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("expected ok=true")
	}
	skills := spec.ExecutorPathToDoneSkills()
	found := false
	for _, s := range skills {
		if s == "commit-push" {
			found = true
		}
		if s == "satelle-story-done-review" {
			t.Errorf("a reviewer-node skill must NOT be a performing skill: %v", skills)
		}
	}
	if !found {
		t.Errorf("named-agent node skill commit-push should be a performing skill, got %v", skills)
	}
}

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
	if byName["in_progress"].Agent != "executor" {
		t.Errorf("in_progress actor = %q, want executor", byName["in_progress"].Agent)
	}
	if byName["committed"].Agent != "reviewer" {
		t.Errorf("committed actor = %q, want reviewer", byName["committed"].Agent)
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
	// The done gate is NO LONGER mandated (sty_9a139c78): a workflow whose edge
	// into done carries no gate still validates — the gate is the user's choice.
	if p := Validate(Spec{States: []State{{Name: "a"}, {Name: "done"}}, Transitions: []Transition{{From: "a", To: "done"}}}); len(p) != 0 {
		t.Errorf("done gate is no longer mandated; should validate, got %v", p)
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
  in_progress [agent=executor]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
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
  in_progress [agent=executor]
  committed   [agent=reviewer, prompt="@skill:satelle-commit-push-reviewer"]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
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
  in_progress [agent=executor]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
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
  - {name: in_progress, agent: executor}
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

// TestStepSummaryNode covers the transparent step-summary declaration
// (sty_9a139c78): a workflow declaring a step node whose gate is the
// step-summary skill, marked mandatory, is reported by Spec.StepSummary.
func TestStepSummaryNode(t *testing.T) {
	withStep := `---
name: x
---
` + "```dot" + `
digraph x {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  step        [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  backlog -> in_progress -> done
}
` + "```" + `
`
	spec, ok := Parse(withStep)
	if !ok {
		t.Fatal("parse failed")
	}
	declared, mandatory := spec.StepSummary()
	if !declared || !mandatory {
		t.Errorf("StepSummary = (%v,%v), want (true,true)", declared, mandatory)
	}
	// The disconnected step node must not desync the start (backlog is first).
	if spec.Start() != "backlog" {
		t.Errorf("Start = %q, want backlog", spec.Start())
	}
	// A workflow without a step node declares no summary.
	noStep, _ := Parse(sampleDOT)
	if d, _ := noStep.StepSummary(); d {
		t.Errorf("sampleDOT declares no step node; StepSummary should be false")
	}
}

func has(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func TestScopedReviewers(t *testing.T) {
	dot := "---\nname: x\n---\n" + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare]
  estimate [agent=reviewer, prompt="@skill:satelle-estimate-actual-review", on="in_progress,done"]
  always   [agent=reviewer, prompt="@skill:rev-all", on="*"]
  step     [agent=reviewer, prompt="@skill:satelle-step-summary", on="*"]
  backlog -> in_progress -> done
}
` + "```" + "\n"
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("parse failed")
	}
	// estimate is scoped to in_progress + done; the wildcard joins every edge; the
	// step summariser is NEVER returned as a blocking scoped gate (it runs via Summarise).
	ip := spec.ScopedReviewers("in_progress")
	if !has(ip, "satelle-estimate-actual-review") || !has(ip, "rev-all") || has(ip, "satelle-step-summary") {
		t.Errorf("in_progress scoped = %v", ip)
	}
	integ := spec.ScopedReviewers("integration")
	if has(integ, "satelle-estimate-actual-review") || !has(integ, "rev-all") || has(integ, "satelle-step-summary") {
		t.Errorf("integration scoped should be wildcard-only (no estimate, no step), got %v", integ)
	}
}

func TestMultiReviewerEdge(t *testing.T) {
	dot := "---\nname: x\n---\n" + "```dot" + `
digraph w {
  in_progress [agent=executor]
  done [shape=Msquare]
  in_progress -> done [reviewer_skill="rev-a,rev-b"]
}
` + "```" + "\n"
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("parse failed")
	}
	var tr Transition
	for _, x := range spec.Transitions {
		if x.From == "in_progress" && x.To == "done" {
			tr = x
		}
	}
	if len(tr.Skills) != 2 || tr.Skills[0] != "rev-a" || tr.Skills[1] != "rev-b" {
		t.Errorf("edge Skills = %v, want [rev-a rev-b]", tr.Skills)
	}
	if tr.Skill != "rev-a" {
		t.Errorf("Skill back-compat = %q, want rev-a", tr.Skill)
	}
}
