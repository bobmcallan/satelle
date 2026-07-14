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

// TestPerformingStates: PerformingStates / IsPerforming / IsPerformingState are the
// single-sourced predicate the edit-gate engaged scan and the dispatch lock-guard
// share. A performing node carries a non-reviewer agent (executor OR a named
// isolated agent); terminal markers and reviewer nodes do not perform.
func TestPerformingStates(t *testing.T) {
	const dot = `---
name: x
---
` + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  plan        [agent=planner, prompt="@skill:plan"]
  in_progress [agent=coder, prompt="@skill:code"]
  review      [agent=reviewer, prompt="@skill:r"]
  done        [shape=Msquare]
  backlog -> plan -> in_progress -> review -> done
}
` + "```" + `
`
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("expected ok=true")
	}
	got := spec.PerformingStates()
	want := map[string]bool{"plan": true, "in_progress": true}
	if len(got) != len(want) {
		t.Fatalf("PerformingStates = %v, want keys %v", got, want)
	}
	for _, s := range got {
		if !want[s] {
			t.Errorf("PerformingStates included non-performing %q: %v", s, got)
		}
	}
	// IsPerformingState: named agent (planner/coder) yes; reviewer/terminal no;
	// unknown state no.
	for name, wantPerform := range map[string]bool{
		"plan": true, "in_progress": true,
		"review": false, "backlog": false, "done": false, "nope": false,
	} {
		if spec.IsPerformingState(name) != wantPerform {
			t.Errorf("IsPerformingState(%q) = %v, want %v", name, spec.IsPerformingState(name), wantPerform)
		}
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

// TestEdgeGateNodeConsistentForm: an edge may declare its gate in the
// node-consistent form (agent=reviewer, prompt="@skill:NAME"), equivalent to
// reviewer_skill="NAME"; reviewer_skill wins when both are present; and a prompt
// without agent=reviewer (or an agent=reviewer without a @skill: prompt) is not a
// gate (sty_be67919a).
func TestEdgeGateNodeConsistentForm(t *testing.T) {
	parse := func(edge string) map[string]string {
		body := "---\nname: b\n---\n" + "```dot" + `
digraph b {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  ` + edge + `
  in_progress -> done
}
` + "```\n"
		spec, ok := Parse(body)
		if !ok {
			t.Fatalf("parse failed for edge %q", edge)
		}
		skill := map[string]string{}
		for _, tr := range spec.Transitions {
			skill[tr.From+"->"+tr.To] = tr.Skill
		}
		return skill
	}

	// Node-consistent form gates the edge exactly like reviewer_skill does.
	if got := parse(`backlog -> in_progress [agent=reviewer, prompt="@skill:satelle-story-intent-review"]`)["backlog->in_progress"]; got != "satelle-story-intent-review" {
		t.Errorf("node-consistent edge gate = %q, want satelle-story-intent-review", got)
	}
	// reviewer_skill wins when both are present.
	if got := parse(`backlog -> in_progress [reviewer_skill="a", agent=reviewer, prompt="@skill:b"]`)["backlog->in_progress"]; got != "a" {
		t.Errorf("both-present precedence = %q, want a (reviewer_skill wins)", got)
	}
	// A prompt WITHOUT agent=reviewer is not an edge gate.
	if got := parse(`backlog -> in_progress [prompt="@skill:nope"]`)["backlog->in_progress"]; got != "" {
		t.Errorf("prompt without agent=reviewer must not gate, got %q", got)
	}
	// agent=reviewer WITHOUT a @skill: prompt is not an edge gate.
	if got := parse(`backlog -> in_progress [agent=reviewer]`)["backlog->in_progress"]; got != "" {
		t.Errorf("agent=reviewer without @skill: prompt must not gate, got %q", got)
	}
	// A node-consistent edge gate into a reviewer node keeps the EDGE's skill,
	// not the target node's derived skill.
	if got := parse(`backlog -> done [agent=reviewer, prompt="@skill:edge-wins"]`)["backlog->done"]; got != "edge-wins" {
		t.Errorf("edge gate into a reviewer node = %q, want edge-wins (edge overrides node)", got)
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
	// Emitter writes the canonical node-consistent edge form, not legacy reviewer_skill=.
	if strings.Contains(out, "reviewer_skill=") {
		t.Errorf("ToDOT must emit node-consistent edge gates, not reviewer_skill=:\n%s", out)
	}
	if !strings.Contains(out, `agent=reviewer, prompt="@skill:satelle-story-intent-review"`) {
		t.Errorf("ToDOT must emit node-consistent intent gate:\n%s", out)
	}
}

// TestParseModelOverride (sty_19456622): node and edge model= parse; absent → "".
func TestParseModelOverride(t *testing.T) {
	dot := "---\nname: x\n---\n```dot\n" + `digraph w {
  backlog [shape=Mdiamond]
  plan [agent=planner, prompt="@skill:plan", model="opus"]
  done [shape=Msquare]
  estimate [agent=reviewer, prompt="@skill:est", on="done", model="sonnet"]
  backlog -> plan [agent=reviewer, prompt="@skill:intent", model="haiku"]
  plan -> done
}
` + "```\n"
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("parse failed")
	}
	byName := map[string]State{}
	for _, s := range spec.States {
		byName[s.Name] = s
	}
	if byName["plan"].Model != "opus" {
		t.Errorf("plan node model = %q, want opus", byName["plan"].Model)
	}
	if byName["estimate"].Model != "sonnet" {
		t.Errorf("estimate node model = %q, want sonnet", byName["estimate"].Model)
	}
	if byName["done"].Model != "" {
		t.Errorf("done model = %q, want empty", byName["done"].Model)
	}
	var edgeModel string
	for _, tr := range spec.Transitions {
		if tr.From == "backlog" && tr.To == "plan" {
			edgeModel = tr.Model
		}
	}
	if edgeModel != "haiku" {
		t.Errorf("edge model = %q, want haiku", edgeModel)
	}
}

// TestEmitCanonicalRoundTrip locks the CANONICAL latest format the emitter
// writes (sty_ccf41efa / satelle-dot-standard): node-consistent edge gates,
// performing-node prompts, never reviewer_skill= — and proves the output
// round-trips through the parser (including multi-skill CSV prompts).
func TestEmitCanonicalRoundTrip(t *testing.T) {
	spec := Spec{
		States: []State{
			{Name: "backlog"},
			{Name: "in_progress", Agent: "executor", Skill: "code"},
			{Name: "done", Terminal: true},
			{Name: "close", Agent: "reviewer", Skill: "done-rev", Model: "opus"},
		},
		Transitions: []Transition{
			{From: "backlog", To: "in_progress", Skill: "intent", Skills: []string{"intent"}},
			{From: "in_progress", To: "done", Skill: "a", Skills: []string{"a", "b"}, Model: "sonnet"},
		},
	}
	out := emitDOT(spec, "w")
	if strings.Contains(out, "reviewer_skill=") {
		t.Errorf("canonical emit must not write reviewer_skill=:\n%s", out)
	}
	if !strings.Contains(out, `prompt="@skill:code"`) {
		t.Errorf("canonical emit must write performing-node prompt:\n%s", out)
	}
	if !strings.Contains(out, `[agent=reviewer, prompt="@skill:intent"]`) {
		t.Errorf("canonical emit must write single-skill node-consistent gate:\n%s", out)
	}
	if !strings.Contains(out, `prompt="@skill:a,b", model="sonnet"`) {
		t.Errorf("canonical emit must write multi-skill gate with model=:\n%s", out)
	}
	if !strings.Contains(out, `model="opus"`) {
		t.Errorf("canonical emit must write node model=:\n%s", out)
	}

	// Round-trip through Parse (wrap in a fenced body).
	body := "---\nname: w\n---\n\n```dot\n" + out + "\n```\n"
	got, ok := Parse(body)
	if !ok {
		t.Fatal("canonical emit must parse as DOT")
	}
	skill := map[string][]string{}
	models := map[string]string{}
	for _, tr := range got.Transitions {
		skills := tr.Skills
		if len(skills) == 0 && tr.Skill != "" {
			skills = []string{tr.Skill}
		}
		skill[tr.From+"->"+tr.To] = skills
		models[tr.From+"->"+tr.To] = tr.Model
	}
	if got := strings.Join(skill["backlog->in_progress"], ","); got != "intent" {
		t.Errorf("single-skill round-trip = %q, want intent", got)
	}
	if got := strings.Join(skill["in_progress->done"], ","); got != "a,b" {
		t.Errorf("multi-skill round-trip = %q, want a,b", got)
	}
	if models["in_progress->done"] != "sonnet" {
		t.Errorf("edge model round-trip = %q, want sonnet", models["in_progress->done"])
	}
	for _, s := range got.States {
		if s.Name == "close" && s.Model != "opus" {
			t.Errorf("node model round-trip = %q, want opus", s.Model)
		}
	}
	byName := map[string]State{}
	for _, s := range got.States {
		byName[s.Name] = s
	}
	if byName["in_progress"].Skill != "code" {
		t.Errorf("performing-node skill round-trip = %q, want code", byName["in_progress"].Skill)
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

func hasScoped(ss []ScopedReviewer, v string) bool {
	for _, s := range ss {
		if s.Skill == v {
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
  estimate [agent=reviewer, prompt="@skill:satelle-estimate-actual-review", on="in_progress,done", model="opus"]
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
	ip := spec.ScopedReviewers("in_progress", nil)
	if !hasScoped(ip, "satelle-estimate-actual-review") || !hasScoped(ip, "rev-all") || hasScoped(ip, "satelle-step-summary") {
		t.Errorf("in_progress scoped = %v", ip)
	}
	for _, s := range ip {
		if s.Skill == "satelle-estimate-actual-review" && s.Model != "opus" {
			t.Errorf("estimate model = %q, want opus", s.Model)
		}
		if s.Skill == "rev-all" && s.Model != "" {
			t.Errorf("rev-all model = %q, want empty", s.Model)
		}
	}
	integ := spec.ScopedReviewers("integration", nil)
	if hasScoped(integ, "satelle-estimate-actual-review") || !hasScoped(integ, "rev-all") || hasScoped(integ, "satelle-step-summary") {
		t.Errorf("integration scoped should be wildcard-only (no estimate, no step), got %v", integ)
	}
}

// TestScopedReviewersAppliesTo (sty_c6d093c8): edge-less reviewer with applies_to
// is enqueued only when tags match; multi-surface both fire; absent applies_to
// matches every story; EqualFold both directions.
func TestScopedReviewersAppliesTo(t *testing.T) {
	dot := "---\nname: x\n---\n" + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare]
  design  [agent=reviewer, prompt="@skill:design-review", on="in_progress", applies_to="surface:ui"]
  cliprobe [agent=reviewer, prompt="@skill:cli-review", on="in_progress", applies_to="surface:cli"]
  always  [agent=reviewer, prompt="@skill:always-gate", on="in_progress"]
  backlog -> in_progress -> done
}
` + "```" + "\n"
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("parse failed")
	}
	// No tags: always-gate only (applies_to-absent); design/cli filtered out.
	none := spec.ScopedReviewers("in_progress", nil)
	if !hasScoped(none, "always-gate") || hasScoped(none, "design-review") || hasScoped(none, "cli-review") {
		t.Errorf("no tags: want always only, got %v", none)
	}
	// surface:ui only
	ui := spec.ScopedReviewers("in_progress", []string{"surface:ui"})
	if !hasScoped(ui, "design-review") || !hasScoped(ui, "always-gate") || hasScoped(ui, "cli-review") {
		t.Errorf("surface:ui: got %v", ui)
	}
	// dual surface — BOTH design and cli (plain filter, no tie-break)
	both := spec.ScopedReviewers("in_progress", []string{"surface:ui", "surface:cli"})
	if !hasScoped(both, "design-review") || !hasScoped(both, "cli-review") || !hasScoped(both, "always-gate") {
		t.Errorf("dual surface: got %v", both)
	}
	// EqualFold: applies_to casing vs tag casing
	fold := spec.ScopedReviewers("in_progress", []string{"Surface:UI"})
	if !hasScoped(fold, "design-review") {
		t.Errorf("EqualFold Surface:UI should match applies_to surface:ui: %v", fold)
	}
	// category / free tag "web" must NOT match surface applies_to
	web := spec.ScopedReviewers("in_progress", []string{"web", "area:web"})
	if hasScoped(web, "design-review") || hasScoped(web, "cli-review") {
		t.Errorf("web tags must not match surface applies_to: %v", web)
	}
}

func TestAppliesToParseAndEmit(t *testing.T) {
	dot := "---\nname: x\n---\n" + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  done [shape=Msquare]
  design [agent=reviewer, prompt="@skill:design", on="done", applies_to="surface:ui,surface:cli"]
  backlog -> done
}
` + "```" + "\n"
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("parse")
	}
	var st State
	for _, s := range spec.States {
		if s.Name == "design" {
			st = s
		}
	}
	if len(st.AppliesTo) != 2 || st.AppliesTo[0] != "surface:ui" || st.AppliesTo[1] != "surface:cli" {
		t.Fatalf("AppliesTo = %v", st.AppliesTo)
	}
	emitted := emitDOT(spec, "w")
	if !strings.Contains(emitted, `applies_to="surface:ui,surface:cli"`) {
		t.Errorf("emitDOT missing applies_to:\n%s", emitted)
	}
	// Round-trip
	spec2, ok := Parse("---\nname: x\n---\n```dot\n" + emitted + "\n```\n")
	if !ok {
		t.Fatal("reparse")
	}
	for _, s := range spec2.States {
		if s.Name == "design" {
			if len(s.AppliesTo) != 2 {
				t.Errorf("round-trip AppliesTo = %v", s.AppliesTo)
			}
		}
	}
}

func TestAppliesToOnEdgeRejected(t *testing.T) {
	dot := "---\nname: x\n---\n" + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  done [shape=Msquare]
  backlog -> done [agent=reviewer, prompt="@skill:rev", applies_to="surface:ui"]
}
` + "```" + "\n"
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("parse")
	}
	probs := Validate(spec)
	if !hasProblem(probs, "applies_to is not honoured on an edge") {
		t.Errorf("want edge applies_to reject, got %v", probs)
	}
}

func TestAppliesToOnPerformingRejected(t *testing.T) {
	dot := "---\nname: x\n---\n" + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code", applies_to="surface:ui"]
  done [shape=Msquare]
  backlog -> in_progress -> done
}
` + "```" + "\n"
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("parse")
	}
	probs := Validate(spec)
	if !hasProblem(probs, "applies_to on performing node") {
		t.Errorf("want performing reject, got %v", probs)
	}
}

func TestUnknownAttrRejected(t *testing.T) {
	dot := "---\nname: x\n---\n" + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  done [shape=Msquare, when="never"]
  backlog -> done
}
` + "```" + "\n"
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("parse")
	}
	probs := Validate(spec)
	if !hasProblem(probs, `unknown node attribute "when"`) {
		t.Errorf("want unknown attr reject, got %v", probs)
	}
}

func TestTagsMatchAppliesTo(t *testing.T) {
	if !tagsMatchAppliesTo(nil, []string{"surface:ui"}) {
		t.Error("empty applies_to should match")
	}
	if !tagsMatchAppliesTo([]string{"*"}, nil) {
		t.Error("* should match")
	}
	if !tagsMatchAppliesTo([]string{"surface:ui"}, []string{"SURFACE:UI"}) {
		t.Error("EqualFold should match")
	}
	if tagsMatchAppliesTo([]string{"surface:ui"}, []string{"surface:cli"}) {
		t.Error("mismatch should not match")
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

// TestNonTerminalEngagingStates verifies that the hook's engagement check reads
// shape markers from the DOT (Mdiamond=start, Msquare=terminal) rather than
// hardcoding state names. This is configuration over code (sty_f3d5d4b8).
func TestNonTerminalEngagingStates(t *testing.T) {
	const dot = `---
name: x
---
` + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  plan        [agent=planner, prompt="@skill:plan"]
  in_progress [agent=worker, prompt="@skill:code"]
  done        [shape=Msquare]
  cancelled   [shape=Msquare, agent=reviewer, prompt="@skill:cancel"]
  backlog -> plan -> in_progress -> done
  plan -> cancelled
}
` + "```" + `
`
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("parse failed")
	}

	engaging := spec.NonTerminalEngagingStates()
	engagingSet := map[string]bool{}
	for _, s := range engaging {
		engagingSet[s] = true
	}

	// Mdiamond (backlog) is the start state — NOT engaging
	if engagingSet["backlog"] {
		t.Errorf("backlog (shape=Mdiamond) should not be engaging, got %v", engaging)
	}

	// Msquare (done, cancelled) are terminal states — NOT engaging
	if engagingSet["done"] {
		t.Errorf("done (shape=Msquare) should not be engaging, got %v", engaging)
	}
	if engagingSet["cancelled"] {
		t.Errorf("cancelled (shape=Msquare) should not be engaging, got %v", engaging)
	}

	// plan and in_progress are neither start nor terminal — engaging
	if !engagingSet["plan"] {
		t.Errorf("plan should be engaging (no shape marker), got %v", engaging)
	}
	if !engagingSet["in_progress"] {
		t.Errorf("in_progress should be engaging (no shape marker), got %v", engaging)
	}

	// Verify shape field is parsed correctly
	byName := map[string]State{}
	for _, s := range spec.States {
		byName[s.Name] = s
	}
	if byName["backlog"].Shape != "Mdiamond" {
		t.Errorf("backlog.Shape = %q, want Mdiamond", byName["backlog"].Shape)
	}
	if byName["done"].Shape != "Msquare" {
		t.Errorf("done.Shape = %q, want Msquare", byName["done"].Shape)
	}
}

// TestNonTerminalEngagingStatesReviewerPark: a park state modeled as
// agent=reviewer with an outgoing resume edge is NOT engaging (so edit/commit
// gates refuse while parked) — without hardcoding any state name.
func TestNonTerminalEngagingStatesReviewerPark(t *testing.T) {
	body := "```dot\n" + `digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  park        [agent=reviewer, prompt="@skill:satelle-story-blocked-review"]
  done        [shape=Msquare]
  backlog -> in_progress -> done
  in_progress -> park
  park -> in_progress
}
` + "```\n"
	spec, ok := Parse(body)
	if !ok {
		t.Fatal("parse")
	}
	engaging := map[string]bool{}
	for _, s := range spec.NonTerminalEngagingStates() {
		engaging[s] = true
	}
	if engaging["park"] {
		t.Errorf("agent=reviewer park state must not be engaging, got %v", spec.NonTerminalEngagingStates())
	}
	if !engaging["in_progress"] {
		t.Errorf("in_progress should remain engaging, got %v", spec.NonTerminalEngagingStates())
	}
}

// TestOnEnterAgentParkStaysNonEngaging: on_enter_agent is orthogonal to Agent —
// a reviewer park with a one-shot entry performer remains non-engaging and
// non-performing (sty_5cabe26f). Uses a non-"blocked" node name so no name
// dependence is implied.
func TestOnEnterAgentParkStaysNonEngaging(t *testing.T) {
	body := "```dot\n" + `digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  parked      [agent=reviewer, prompt="@skill:park-gate", on_enter_agent=triage, on_enter_prompt="@skill:triage-skill"]
  done        [shape=Msquare]
  backlog -> in_progress -> done
  in_progress -> parked [agent=reviewer, prompt="@skill:park-gate"]
  parked -> in_progress
}
` + "```\n"
	spec, ok := Parse(body)
	if !ok {
		t.Fatal("parse")
	}
	var park *State
	for i := range spec.States {
		if spec.States[i].Name == "parked" {
			park = &spec.States[i]
			break
		}
	}
	if park == nil {
		t.Fatal("parked state missing")
	}
	if park.Agent != "reviewer" || park.Skill != "park-gate" {
		t.Errorf("park role/gate: agent=%q skill=%q", park.Agent, park.Skill)
	}
	if park.OnEnterAgent != "triage" || park.OnEnterSkill != "triage-skill" {
		t.Errorf("on_enter: agent=%q skill=%q", park.OnEnterAgent, park.OnEnterSkill)
	}
	if park.IsPerforming() {
		t.Error("on_enter_agent must not make the node performing")
	}
	for _, s := range spec.NonTerminalEngagingStates() {
		if s == "parked" {
			t.Errorf("park with on_enter_agent must not be engaging, got %v", spec.NonTerminalEngagingStates())
		}
	}
	// Edge gate into park still resolves (AC4).
	for _, tr := range spec.Transitions {
		if tr.To == "parked" && tr.Skill != "park-gate" {
			t.Errorf("entry edge gate = %q, want park-gate", tr.Skill)
		}
	}
}

// TestNonTerminalEngagingStatesCancelSink closes the gap TestNonTerminalEngagingStates
// masks: that test's cancelled node carries shape=Msquare, so the Msquare branch
// catches it and the reviewer-sink branch (the REAL authored cancel shape — no shape
// marker, agent=reviewer, incoming edges only, no outgoing) is never exercised. This
// mirrors satelle-project-workflow's cancelled node EXACTLY (sty_f3d5d4b8): the old
// inDegree==0 (no INCOMING edges) check counted it engaging because cancelled has
// several incoming edges; the corrected st.Terminal (no OUTGOING edges) check must NOT.
func TestNonTerminalEngagingStatesCancelSink(t *testing.T) {
	const dot = `---
name: x
---
` + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  plan        [agent=planner, prompt="@skill:plan"]
  in_progress [agent=worker, prompt="@skill:code"]
  done        [shape=Msquare]
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  backlog -> plan -> in_progress -> done
  backlog -> cancelled
  plan -> cancelled
  in_progress -> cancelled
}
` + "```" + `
`
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("parse failed")
	}
	byName := map[string]State{}
	for _, s := range spec.States {
		byName[s.Name] = s
	}
	// The real authored cancel shape: no shape marker, agent=reviewer, incoming edges, no outgoing.
	if byName["cancelled"].Shape != "" {
		t.Fatalf("cancelled must carry no shape marker for this test, got %q", byName["cancelled"].Shape)
	}
	if byName["cancelled"].Agent != "reviewer" {
		t.Fatalf("cancelled agent = %q, want reviewer", byName["cancelled"].Agent)
	}
	if !byName["cancelled"].Terminal {
		t.Fatal("cancelled must be terminal (no outgoing edges) for the sink check")
	}

	engagingSet := map[string]bool{}
	for _, s := range spec.NonTerminalEngagingStates() {
		engagingSet[s] = true
	}
	// A cancelled story must NOT count as engaged — it is a terminal exit (no outgoing
	// edges), even though it has incoming edges and carries no shape marker.
	if engagingSet["cancelled"] {
		t.Errorf("cancelled (agent=reviewer, no shape, no outgoing edges) must NOT be engaging — got %v",
			spec.NonTerminalEngagingStates())
	}
	// The in-flight performing states still engage.
	if !engagingSet["plan"] || !engagingSet["in_progress"] {
		t.Errorf("plan and in_progress should be engaging, got %v", spec.NonTerminalEngagingStates())
	}
}

// TestExecutorAugmentation (sty_8225d8a5): edge-less executor with on= + applies_to
// composes onto the spine skill; dual surface gets both; engagement skill set is
// surface-aware; augmentation is not engaging / not a performing spine state.
func TestExecutorAugmentation(t *testing.T) {
	dot := "---\nname: x\n---\n" + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done [shape=Msquare]
  codeui [agent=executor, prompt="@skill:code-ui", on="in_progress", applies_to="surface:ui"]
  codecli [agent=executor, prompt="@skill:code-cli", on="in_progress", applies_to="surface:cli"]
  backlog -> in_progress -> done
}
` + "```" + "\n"
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("parse")
	}
	if probs := Validate(spec); len(probs) > 0 {
		t.Fatalf("validate: %v", probs)
	}
	// Spine graph: augmentation names must not appear in PerformingStates.
	for _, n := range spec.PerformingStates() {
		if n == "codeui" || n == "codecli" {
			t.Errorf("PerformingStates includes augmentation %q", n)
		}
	}
	for _, n := range spec.NonTerminalEngagingStates() {
		if n == "codeui" || n == "codecli" {
			t.Errorf("engaging includes augmentation %q", n)
		}
	}
	// Skills composition
	none := spec.ExecutorSkillsFor("in_progress", nil)
	if len(none) != 1 || none[0] != "code" {
		t.Errorf("no tags: got %v", none)
	}
	ui := spec.ExecutorSkillsFor("in_progress", []string{"surface:ui"})
	if len(ui) != 2 || ui[0] != "code" || ui[1] != "code-ui" {
		t.Errorf("ui: got %v", ui)
	}
	both := spec.ExecutorSkillsFor("in_progress", []string{"surface:ui", "surface:cli"})
	if len(both) != 3 || both[0] != "code" || both[1] != "code-ui" || both[2] != "code-cli" {
		t.Errorf("both (declaration order): got %v, want [code code-ui code-cli]", both)
	}
	// Tag order does not change composition order.
	bothRev := spec.ExecutorSkillsFor("in_progress", []string{"surface:cli", "surface:ui"})
	if len(bothRev) != 3 || bothRev[1] != "code-ui" || bothRev[2] != "code-cli" {
		t.Errorf("tag order must not reorder augs: got %v", bothRev)
	}
	// Unknown / non-spine target → nil
	if got := spec.ExecutorSkillsFor("codeui", []string{"surface:ui"}); got != nil {
		t.Errorf("augmentation name is not a spine target: %v", got)
	}
	if got := spec.ExecutorSkillsFor("nope", nil); got != nil {
		t.Errorf("unknown state: %v", got)
	}
	// Path skills: structure gets all augs; filtered engagement does not.
	all := spec.ExecutorPathToDoneSkills()
	if !containsStr(all, "code-ui") || !containsStr(all, "code-cli") {
		t.Errorf("all path skills: %v", all)
	}
	cliOnly := spec.ExecutorPathToDoneSkillsFor([]string{"surface:cli"})
	if containsStr(cliOnly, "code-ui") || !containsStr(cliOnly, "code-cli") || !containsStr(cliOnly, "code") {
		t.Errorf("cli path skills: %v", cliOnly)
	}
	// Graph equality: transitions identical regardless of surface (spine unchanged).
	if len(spec.Transitions) != 2 {
		t.Errorf("spine edges: %d", len(spec.Transitions))
	}
	// emitDOT round-trip
	em := emitDOT(spec, "w")
	if !strings.Contains(em, `on="in_progress"`) || !strings.Contains(em, `applies_to="surface:ui"`) {
		t.Errorf("emit missing aug attrs:\n%s", em)
	}
}

func TestAugmentationAppliesToAllowed(t *testing.T) {
	dot := "---\nname: x\n---\n" + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done [shape=Msquare]
  codeui [agent=executor, prompt="@skill:code-ui", on="in_progress", applies_to="surface:ui"]
  backlog -> in_progress -> done
}
` + "```" + "\n"
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("parse")
	}
	if probs := Validate(spec); len(probs) > 0 {
		t.Fatalf("augmentation with applies_to should validate: %v", probs)
	}
}

func TestSpineWithOnRejected(t *testing.T) {
	dot := "---\nname: x\n---\n" + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code", on="in_progress"]
  done [shape=Msquare]
  backlog -> in_progress -> done
}
` + "```" + "\n"
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("parse")
	}
	probs := Validate(spec)
	if !hasProblem(probs, "participates in edges") {
		t.Errorf("want spine+on= reject, got %v", probs)
	}
}

func TestScopedReviewersSplitSkipped(t *testing.T) {
	dot := "---\nname: x\n---\n" + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare]
  design [agent=reviewer, prompt="@skill:design", on="in_progress", applies_to="surface:ui"]
  always [agent=reviewer, prompt="@skill:always", on="in_progress"]
  backlog -> in_progress -> done
}
` + "```" + "\n"
	spec, ok := Parse(dot)
	if !ok {
		t.Fatal("parse")
	}
	en, sk := spec.ScopedReviewersSplit("in_progress", []string{"surface:cli"})
	if !hasScoped(en, "always") || hasScoped(en, "design") {
		t.Errorf("enqueued=%v", en)
	}
	if !hasScoped(sk, "design") {
		t.Errorf("skipped should include design: %v", sk)
	}
	// Matching tags → nothing skipped
	_, sk2 := spec.ScopedReviewersSplit("in_progress", []string{"surface:ui"})
	if len(sk2) != 0 {
		t.Errorf("no skip when match: %v", sk2)
	}
}
