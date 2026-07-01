package reviewer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const testWorkflow = `
transitions:
  - {from: backlog, to: in_progress, reviewer_skill: "satelle-story-intent-review"}
  - {from: in_progress, to: done, reviewer_skill: "satelle-story-done-review"}
  - {from: backlog, to: cancelled}
`

type fakeRunner struct {
	out string
	err error
	got agentcli.Request
}

func (f *fakeRunner) Name() string    { return "fake" }
func (f *fakeRunner) Command() string { return "fake -p --append-system-prompt {system}" }
func (f *fakeRunner) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	f.got = req
	return []byte(f.out), f.err
}

type fakeDocs struct {
	workflow   string
	skillBody  string
	skillFound bool
	// extraWorkflows are returned by List in addition to the baseline — used to
	// exercise category→workflow selection via applies_to.
	extraWorkflows []docindex.Doc
	// extraSkills are returned by List("skills") and resolved by Get("skills",…) —
	// used to exercise the always-on system reviewer layer (tagged reviewer:always)
	// and per-skill reviewer bodies.
	extraSkills []docindex.Doc
	// extraPrinciples are returned by List("principles") — used to exercise the
	// always-resident principle injection into the reviewer system prompt.
	extraPrinciples []docindex.Doc
}

func (d fakeDocs) Get(_ context.Context, kind, name string) (docindex.Doc, error) {
	switch kind {
	case "workflows":
		if name == baselineWorkflow {
			return docindex.Doc{Kind: kind, Name: name, Body: d.workflow}, nil
		}
		for _, w := range d.extraWorkflows {
			if w.Name == name {
				return w, nil
			}
		}
	case "skills":
		for _, s := range d.extraSkills {
			if s.Name == name {
				return s, nil
			}
		}
		if d.skillFound {
			return docindex.Doc{Kind: kind, Name: name, Body: d.skillBody}, nil
		}
		return docindex.Doc{}, docindex.ErrNotFound
	case "principles":
		for _, p := range d.extraPrinciples {
			if p.Name == name {
				return p, nil
			}
		}
	}
	return docindex.Doc{}, docindex.ErrNotFound
}

func (d fakeDocs) List(_ context.Context, kind string) ([]docindex.Doc, error) {
	switch kind {
	case "workflows":
		out := []docindex.Doc{{Kind: "workflows", Name: baselineWorkflow, Body: d.workflow}}
		return append(out, d.extraWorkflows...), nil
	case "skills":
		return d.extraSkills, nil
	case "principles":
		return d.extraPrinciples, nil
	}
	return nil, nil
}

const alwaysPrincipleDoc = `---
name: satelle-test-belief
kind: principle
tags: [kind:principle, principles:session]
---
# Test belief

This resident belief MUST be visible to every reviewer.`

// secondAlwaysDoc is a SECOND principles:session principle (not the operating one)
// — proves the reviewer injects the full session SET, matching SessionStart, not
// just config.OperatingPrinciple. Its prose deliberately omits the literal tag so
// the frontmatter-stripped assertion below still holds.
const secondAlwaysDoc = `---
name: satelle-second-resident
type: principle
tags: [type:principle, principles:session]
---
# Second belief

The full resident SET must be injected, not just the operating principle.`

// TestReviewerSystemPromptInjectsPrinciplesAndCTA: a reviewer's system prompt
// carries the always-resident principles, the read-only call-to-action (teaching
// it to resolve substrate via the satelle CLI), and its own rubric.
func TestReviewerSystemPromptInjectsPrinciplesAndCTA(t *testing.T) {
	docs := fakeDocs{
		workflow:   testWorkflow,
		skillBody:  "rubric body",
		skillFound: true,
		extraPrinciples: []docindex.Doc{
			// The full principles:session SET is injected (operating principle + any
			// other session-tagged principle); a non-tagged principle is not.
			{Kind: "principles", Name: config.OperatingPrinciple, Body: alwaysPrincipleDoc},
			{Kind: "principles", Name: "satelle-second-resident", Body: secondAlwaysDoc},
			{Kind: "principles", Name: "satelle-not-resident", Body: "---\nname: x\ntype: principle\n---\nnot resident"},
		},
	}
	g, r := gater(t, `{"decision":"accept"}`, docs)
	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "done"); err != nil {
		t.Fatal(err)
	}
	sp := r.got.SystemPrompt
	if !strings.Contains(sp, "This resident belief MUST be visible") {
		t.Errorf("operating always-resident principle not injected:\n%s", sp)
	}
	if !strings.Contains(sp, "The full resident SET must be injected") {
		t.Errorf("the full principles:session SET not injected (second resident missing):\n%s", sp)
	}
	if strings.Contains(sp, "not resident") {
		t.Errorf("a non-resident principle must NOT be injected:\n%s", sp)
	}
	if !strings.Contains(sp, "read-only") || !strings.Contains(sp, ".satelle/") {
		t.Errorf("call-to-action (read-only, reads materialised .satelle substrate) missing:\n%s", sp)
	}
	if !strings.Contains(sp, "rubric body") {
		t.Errorf("the reviewer's own rubric must still ride in the prompt:\n%s", sp)
	}
	// Frontmatter of the injected principle must be stripped (no raw tags line).
	if strings.Contains(sp, "principles:session") {
		t.Errorf("injected principle frontmatter should be stripped:\n%s", sp)
	}
}

// TestReviewerSystemPromptOmitsPrinciplesWhenDisabled: the agents-layer toggle
// (default ON) omits the resident principles when turned off, while the reviewer's
// own rubric and the call-to-action still ride (sty_46a40208).
func TestReviewerSystemPromptOmitsPrinciplesWhenDisabled(t *testing.T) {
	docs := fakeDocs{
		workflow:   testWorkflow,
		skillBody:  "rubric body",
		skillFound: true,
		extraPrinciples: []docindex.Doc{
			{Kind: "principles", Name: config.OperatingPrinciple, Body: alwaysPrincipleDoc},
		},
	}
	g, r := gater(t, `{"decision":"accept"}`, docs)
	g.SetInjectPrinciples(false) // disable injection for this agent
	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "done"); err != nil {
		t.Fatal(err)
	}
	sp := r.got.SystemPrompt
	if strings.Contains(sp, "This resident belief MUST be visible") {
		t.Errorf("principles injected despite the toggle being off:\n%s", sp)
	}
	if !strings.Contains(sp, "rubric body") || !strings.Contains(sp, "read-only") {
		t.Errorf("rubric + call-to-action must still ride when injection is off:\n%s", sp)
	}
}

func TestStripFrontmatter(t *testing.T) {
	got := stripFrontmatter("---\nname: x\ntags: [a]\n---\n# Body\n\ntext")
	if strings.Contains(got, "name:") || !strings.Contains(got, "# Body") {
		t.Errorf("stripFrontmatter = %q", got)
	}
	if got := stripFrontmatter("no frontmatter here"); got != "no frontmatter here" {
		t.Errorf("body without frontmatter should pass through, got %q", got)
	}
}

func skillDoc(name string) docindex.Doc {
	return docindex.Doc{Kind: "skills", Name: name, Body: "rubric body"}
}

// engageDOT is a valid DOT workflow whose start state is backlog. Its path to done
// runs through an executor step (commit_push) with an @skill: prompt — the thing
// the engagement guard resolves.
const engageDOT = "```dot\n" + `digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  commit_push [agent=executor, prompt="@skill:commit-push"]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  backlog -> in_progress [reviewer_skill="satelle-story-intent-review"]
  in_progress -> commit_push
  commit_push -> done
  backlog -> cancelled
}
` + "```\n"

// TestEngagementBlockedWhenExecutorSkillMissing: engaging under a workflow whose
// path to done has an executor step with an unresolvable skill is rejected up
// front (deterministically, no agent), naming the missing skill.
func TestEngagementBlockedWhenExecutorSkillMissing(t *testing.T) {
	// Only the intent + done reviewers resolve; the executor skill commit-push does NOT.
	docs := fakeDocs{workflow: engageDOT, extraSkills: []docindex.Doc{
		skillDoc("satelle-story-intent-review"),
		skillDoc("satelle-story-done-review"),
	}}
	g, _ := gater(t, `{"decision":"accept"}`, docs)
	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if dec.Accept {
		t.Fatal("expected engagement blocked (commit-push missing), got accept")
	}
	if dec.Skill != "satelle-workflow-skill-check" {
		t.Errorf("blocking skill = %q, want satelle-workflow-skill-check", dec.Skill)
	}
	if !strings.Contains(dec.Notes, "commit-push") {
		t.Errorf("reject notes should name the missing executor skill: %q", dec.Notes)
	}
	if strings.Contains(dec.Notes, "satelle-story-cancel-review") {
		t.Errorf("a reviewer gate on the cancel exit must NOT be required: %q", dec.Notes)
	}
}

// TestEngagementProceedsWhenExecutorSkillsResolve: when every executor skill on
// the path to done resolves, the guard passes and the edge proceeds normally.
func TestEngagementProceedsWhenExecutorSkillsResolve(t *testing.T) {
	docs := fakeDocs{workflow: engageDOT, skillBody: "rubric", skillFound: true}
	g, _ := gater(t, `{"decision":"accept"}`, docs)
	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Accept {
		t.Fatalf("expected engagement to proceed (executor skills resolve), got %+v", dec)
	}
}

// TestEngagementGuardSkippedOffEngagementEdge: the executor guard fires only on
// the engagement edge — a later transition (in_progress->done) does not run it.
func TestEngagementGuardSkippedOffEngagementEdge(t *testing.T) {
	// commit-push is missing; if the guard ran off-edge it would block. It must not.
	docs := fakeDocs{workflow: engageDOT, skillBody: "rubric", skillFound: true}
	g, _ := gater(t, `{"decision":"accept"}`, docs)
	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "commit_push")
	if err != nil {
		t.Fatal(err)
	}
	if dec.Skill == "satelle-workflow-skill-check" {
		t.Errorf("executor guard must not run off the engagement edge; skill=%q", dec.Skill)
	}
}

func gater(t *testing.T, out string, docs fakeDocs) (*Gater, *fakeRunner) {
	t.Helper()
	r := &fakeRunner{out: out}
	return New(r, docs, "/repo", ""), r
}

var errFakeAgent = errors.New("fake agent failure")

// scriptedRunner returns a queued sequence of (out, err) — one per Run call — to
// exercise transient reviewer failures (sty_d71b0791). Once exhausted it repeats
// the last result.
type scriptedRunner struct {
	results []struct {
		out string
		err error
	}
	calls int
}

func (s *scriptedRunner) Name() string    { return "scripted" }
func (s *scriptedRunner) Command() string { return "scripted" }
func (s *scriptedRunner) Run(_ context.Context, _ agentcli.Request) ([]byte, error) {
	i := s.calls
	s.calls++
	if i >= len(s.results) {
		i = len(s.results) - 1
	}
	return []byte(s.results[i].out), s.results[i].err
}

// A transient no-verdict reviewer result (an empty/garbled/rate-limited subprocess
// under concurrent load) is RETRIED with backoff, so the gate still advances
// rather than failing on the first shot (sty_d71b0791).
func TestGate_retriesTransientNoVerdictThenAdvances(t *testing.T) {
	docs := fakeDocs{workflow: testWorkflow, skillBody: "rubric body", skillFound: true}
	r := &scriptedRunner{results: []struct {
		out string
		err error
	}{
		{out: "rate limited, please retry"}, // no verdict → transient
		{err: errFakeAgent},                 // agent error → transient
		{out: `{"decision":"accept"}`},      // verdict on the 3rd try
	}}
	g := New(r, docs, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 } // no real waits in the test

	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "done")
	if err != nil {
		t.Fatalf("gate should advance after a transient retry, got err: %v", err)
	}
	if !dec.Accept {
		t.Fatalf("gate should accept once the reviewer returns a verdict: %+v", dec)
	}
	if r.calls != 3 {
		t.Fatalf("expected 3 reviewer attempts (2 transient + 1 verdict), got %d", r.calls)
	}
}

// When every attempt fails to produce a verdict, the gate surfaces a CLEAR error
// (naming the retry exhaustion) rather than a silent non-advance — the transition
// is deterministic (sty_d71b0791).
func TestGate_clearErrorWhenNoVerdictAfterRetries(t *testing.T) {
	docs := fakeDocs{workflow: testWorkflow, skillBody: "rubric body", skillFound: true}
	r := &scriptedRunner{results: []struct {
		out string
		err error
	}{{out: "still no verdict"}}}
	g := New(r, docs, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }

	_, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "done")
	if err == nil {
		t.Fatal("expected a clear error when the reviewer never returns a verdict")
	}
	if !strings.Contains(err.Error(), "no verdict after") {
		t.Errorf("error should name the no-verdict retry exhaustion, got: %v", err)
	}
	if r.calls != defaultReviewerAttempts {
		t.Errorf("expected %d attempts, got %d", defaultReviewerAttempts, r.calls)
	}
}

func TestGateAcceptEnacts(t *testing.T) {
	g, r := gater(t, `the story is ready {"decision":"accept","notes":"looks good"} done`,
		fakeDocs{workflow: testWorkflow, skillBody: "rubric body", skillFound: true})
	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "done")
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Gated || !dec.Accept {
		t.Fatalf("want gated accept, got %+v", dec)
	}
	if dec.Skill != "satelle-story-done-review" {
		t.Errorf("skill = %q", dec.Skill)
	}
	if !strings.Contains(r.got.SystemPrompt, "rubric body") {
		t.Errorf("skill body should ride in the system prompt, got %q", r.got.SystemPrompt)
	}
	if r.got.Dir != "/repo" {
		t.Errorf("reviewer should run in repo root, got %q", r.got.Dir)
	}
}

func TestGateRejectBlocks(t *testing.T) {
	g, _ := gater(t, `{"decision":"reject","notes":"no acceptance criteria"}`,
		fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	dec, err := g.Gate(context.Background(), workitem.Item{Status: "in_progress"}, "done")
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Gated || dec.Accept {
		t.Fatalf("want gated reject, got %+v", dec)
	}
	if dec.Notes != "no acceptance criteria" {
		t.Errorf("notes = %q", dec.Notes)
	}
}

func TestUngatedEdgeIsAdvisory(t *testing.T) {
	// backlog→cancelled has no reviewer_skill — must not gate (and must not run).
	g, r := gater(t, `{"decision":"reject"}`,
		fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	dec, err := g.Gate(context.Background(), workitem.Item{Status: "backlog"}, "cancelled")
	if err != nil {
		t.Fatal(err)
	}
	if dec.Gated {
		t.Errorf("ungated edge should report Gated=false, got %+v", dec)
	}
	if r.got.SystemPrompt != "" {
		t.Errorf("reviewer must not run on an ungated edge")
	}
}

func TestNamedSkillButRubricAbsentIsAdvisory(t *testing.T) {
	// Workflow names a reviewer skill, but its rubric is not installed — advisory
	// until it ships (keeps fresh repos / pre-A4 working).
	g, r := gater(t, `{"decision":"reject"}`,
		fakeDocs{workflow: testWorkflow, skillFound: false})
	dec, err := g.Gate(context.Background(), workitem.Item{Status: "in_progress"}, "done")
	if err != nil {
		t.Fatal(err)
	}
	if dec.Gated {
		t.Errorf("absent rubric should be advisory, got %+v", dec)
	}
	if r.got.SystemPrompt != "" {
		t.Errorf("reviewer must not run without a rubric")
	}
}

func TestBadDecisionErrors(t *testing.T) {
	g, _ := gater(t, `no json here`,
		fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	if _, err := g.Gate(context.Background(), workitem.Item{Status: "in_progress"}, "done"); err == nil {
		t.Fatal("expected error on unparseable reviewer output")
	}
}

func TestReviewerSkillsFor(t *testing.T) {
	if got, declared := reviewerSkillsFor(testWorkflow, "in_progress", "done"); len(got) != 1 || got[0] != "satelle-story-done-review" || !declared {
		t.Errorf("in_progress→done = (%v, %v), want ([done-review], true)", got, declared)
	}
	if got, declared := reviewerSkillsFor(testWorkflow, "backlog", "cancelled"); len(got) != 0 || !declared {
		t.Errorf("declared ungated edge = (%v, %v), want (nil, true)", got, declared)
	}
	if got, declared := reviewerSkillsFor(testWorkflow, "backlog", "nowhere"); len(got) != 0 || declared {
		t.Errorf("undeclared edge = (%v, %v), want (nil, false)", got, declared)
	}
	// An ordered list: reviewer_skills takes precedence and preserves order.
	multi := "transitions:\n  - {from: deployed, to: done, reviewer_skills: [first-review, second-review]}\n"
	if got, declared := reviewerSkillsFor(multi, "deployed", "done"); len(got) != 2 || got[0] != "first-review" || got[1] != "second-review" || !declared {
		t.Errorf("reviewer_skills list = (%v, %v), want ([first-review second-review], true)", got, declared)
	}
}

// mapRunner returns a verdict keyed by the review_skill in the reviewer payload
// and records the order skills were invoked — so a test can drive distinct
// per-reviewer verdicts and assert run order / short-circuit.
type mapRunner struct {
	verdict map[string]string
	seen    []string
}

func (m *mapRunner) Name() string    { return "map" }
func (m *mapRunner) Command() string { return "map -p --append-system-prompt {system}" }
func (m *mapRunner) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	var p struct {
		ReviewSkill string `json:"review_skill"`
	}
	_ = json.Unmarshal([]byte(req.Payload), &p)
	m.seen = append(m.seen, p.ReviewSkill)
	out := m.verdict[p.ReviewSkill]
	if out == "" {
		out = `{"decision":"accept"}`
	}
	return []byte(out), nil
}

func TestGateMultipleReviewersAllAccept(t *testing.T) {
	wf := "transitions:\n  - {from: in_progress, to: done, reviewer_skills: [rev-a, rev-b, rev-c]}\n"
	mr := &mapRunner{}
	g := New(mr, fakeDocs{workflow: wf, skillBody: "rubric", skillFound: true}, "/repo", "")
	dec, err := g.Gate(context.Background(), workitem.Item{Status: "in_progress"}, "done")
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Gated || !dec.Accept {
		t.Fatalf("want gated accept, got %+v", dec)
	}
	if len(dec.Reviewers) != 3 {
		t.Fatalf("want 3 reviewer verdicts, got %d: %+v", len(dec.Reviewers), dec.Reviewers)
	}
	for i, want := range []string{"rev-a", "rev-b", "rev-c"} {
		rv := dec.Reviewers[i]
		if rv.Skill != want || rv.Order != i || !rv.Accept || rv.System {
			t.Errorf("reviewer[%d] = %+v, want skill %s order %d accept non-system", i, rv, want, i)
		}
	}
	if len(mr.seen) != 3 {
		t.Errorf("all reviewers should run when all accept, ran %v", mr.seen)
	}
}

func TestGateMultipleReviewersRejectAttributedAndShortCircuits(t *testing.T) {
	wf := "transitions:\n  - {from: in_progress, to: done, reviewer_skills: [rev-a, rev-b, rev-c]}\n"
	mr := &mapRunner{verdict: map[string]string{"rev-b": `{"decision":"reject","notes":"b says no"}`}}
	g := New(mr, fakeDocs{workflow: wf, skillBody: "rubric", skillFound: true}, "/repo", "")
	dec, err := g.Gate(context.Background(), workitem.Item{Status: "in_progress"}, "done")
	if err != nil {
		t.Fatal(err)
	}
	if dec.Accept {
		t.Fatalf("a reject must block the edge, got %+v", dec)
	}
	if dec.Skill != "rev-b" || dec.Notes != "b says no" {
		t.Errorf("reject should be attributed to rev-b, got skill=%q notes=%q", dec.Skill, dec.Notes)
	}
	if len(dec.Reviewers) != 2 || !dec.Reviewers[0].Accept || dec.Reviewers[1].Accept {
		t.Fatalf("want [accept(rev-a), reject(rev-b)], got %+v", dec.Reviewers)
	}
	if len(mr.seen) != 2 || mr.seen[1] != "rev-b" {
		t.Errorf("rev-c must not run after rev-b rejected; ran %v", mr.seen)
	}
}

// scopedDOT wraps a digraph body in the frontmatter + fenced ```dot envelope a
// workflow doc carries, so wfdot.Parse resolves it.
func scopedDOT(graph string) string {
	return "---\nname: satelle-baseline-workflow\n---\n" + "```dot" + "\n" + graph + "\n" + "```" + "\n"
}

func TestGateScopedReviewerRunsLast(t *testing.T) {
	// A DECLARED scoped reviewer node (edge-less, on="done") runs AFTER the
	// edge-named reviewer — last in order, flagged System. This replaces the removed
	// reviewer:always tag layer: the DOT, not a skill tag, declares the gate
	// (sty_ca9f675f).
	wf := scopedDOT(`digraph t {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare]
  estimate [agent=reviewer, prompt="@skill:satelle-estimate-actual", on="done"]
  backlog -> in_progress
  in_progress -> done [reviewer_skill="satelle-story-done-review"]
}`)
	mr := &mapRunner{}
	g := New(mr, fakeDocs{workflow: wf, skillBody: "rubric", skillFound: true}, "/repo", "")
	dec, err := g.Gate(context.Background(), workitem.Item{Status: "in_progress"}, "done")
	if err != nil {
		t.Fatal(err)
	}
	if len(dec.Reviewers) != 2 {
		t.Fatalf("want 2 reviewers (edge + scoped), got %+v", dec.Reviewers)
	}
	first, last := dec.Reviewers[0], dec.Reviewers[1]
	if first.Skill != "satelle-story-done-review" || first.System {
		t.Errorf("first reviewer should be the edge-named one, got %+v", first)
	}
	if last.Skill != "satelle-estimate-actual" || !last.System || last.Order != 1 {
		t.Errorf("scoped reviewer should run last and be flagged System, got %+v", last)
	}
}

func TestScopedReviewerByOnList(t *testing.T) {
	// A scoped reviewer node (on="done") joins the close edge but is skipped on an
	// unlisted edge — declared in the DOT, so it costs nothing in between and the
	// workflow remains the sole gating authority.
	wf := scopedDOT(`digraph t {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  reviewed [agent=executor]
  done [shape=Msquare]
  estimate [agent=reviewer, prompt="@skill:satelle-estimate-actual", on="done"]
  backlog -> in_progress
  in_progress -> reviewed [reviewer_skill="satelle-story-code-review"]
  reviewed -> done [reviewer_skill="satelle-story-done-review"]
}`)
	docs := fakeDocs{workflow: wf, skillBody: "rubric", skillFound: true}

	// to=reviewed is NOT in the scoped node's on-list → only the edge reviewer runs.
	g1 := New(&mapRunner{}, docs, "/repo", "")
	dec1, err := g1.Gate(context.Background(), workitem.Item{Status: "in_progress"}, "reviewed")
	if err != nil {
		t.Fatal(err)
	}
	if len(dec1.Reviewers) != 1 || dec1.Reviewers[0].Skill != "satelle-story-code-review" {
		t.Fatalf("reviewed edge should run only the edge reviewer, got %+v", dec1.Reviewers)
	}

	// to=done IS in the on-list → the scoped reviewer joins, last.
	g2 := New(&mapRunner{}, docs, "/repo", "")
	dec2, err := g2.Gate(context.Background(), workitem.Item{Status: "reviewed"}, "done")
	if err != nil {
		t.Fatal(err)
	}
	if len(dec2.Reviewers) != 2 || !dec2.Reviewers[1].System || dec2.Reviewers[1].Skill != "satelle-estimate-actual" {
		t.Fatalf("done edge should add the scoped reviewer last, got %+v", dec2.Reviewers)
	}
}

func TestGateRefusesUndeclaredEdge(t *testing.T) {
	// in_progress→integrated is NOT a declared edge in testWorkflow. The gate must
	// refuse it (error) so a story cannot skip a gate by jumping across an
	// undeclared edge — and the reviewer must not run.
	g, r := gater(t, `{"decision":"accept"}`,
		fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	_, err := g.Gate(context.Background(), workitem.Item{Status: "in_progress"}, "integrated")
	if err == nil {
		t.Fatal("expected an error refusing the undeclared in_progress→integrated edge")
	}
	if r.got.SystemPrompt != "" {
		t.Errorf("reviewer must not run on an undeclared edge")
	}
}

// ReviewCreate is now DETERMINISTIC (internal/structure) — no rubric, no agent
// CLI. A well-formed draft accepts; one missing the goal or numbered ACs rejects.
func TestReviewCreateAcceptAndReject(t *testing.T) {
	ctx := context.Background()
	g, _ := gater(t, "", fakeDocs{})

	good := verb.CreateDraft{Kind: "story", Title: "Add X", Body: "Make the thing do X", AcceptanceCriteria: "1. a"}
	dec, err := g.ReviewCreate(ctx, good)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Gated || !dec.Accept || dec.Skill != structureSkill {
		t.Fatalf("want gated accept by %s, got %+v", structureSkill, dec)
	}

	bad := verb.CreateDraft{Kind: "story", Title: "x"} // no goal body, no numbered ACs
	dec2, err := g.ReviewCreate(ctx, bad)
	if err != nil {
		t.Fatal(err)
	}
	if !dec2.Gated || dec2.Accept || dec2.Notes == "" {
		t.Fatalf("want gated reject with notes, got %+v", dec2)
	}
}

// The create check is ALWAYS gated (structure is the one thing satelle enforces
// on creation) and never depends on a rubric being installed or an agent runner.
func TestReviewCreateAlwaysGatedNoRunner(t *testing.T) {
	g, r := gater(t, "", fakeDocs{skillFound: false})
	dec, err := g.ReviewCreate(context.Background(), verb.CreateDraft{Kind: "story", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Gated || dec.Accept {
		t.Errorf("want deterministic gated reject, got %+v", dec)
	}
	if r.got.SystemPrompt != "" {
		t.Errorf("create check must not run an agent")
	}
}

// validDraft is structurally conformant (clear goal + a numbered AC) so the
// content reviewer is reached.
var validDraft = verb.CreateDraft{Kind: "story", Title: "Add X", Body: "Make the thing do X", AcceptanceCriteria: "1. it does X"}

// createWF is a wildcard workflow that DECLARES a content/alignment create
// reviewer via its create_review frontmatter (sty_b031b29f) — the binding lives on
// the workflow, not a hardcoded constant.
const createWF = "---\nname: " + baselineWorkflow + "\ntype: workflow\napplies_to: [\"*\"]\ncreate_review: my-create-review\n---\n# wf\n"

// plainWF is a wildcard workflow with NO create_review declaration.
const plainWF = "---\nname: " + baselineWorkflow + "\ntype: workflow\napplies_to: [\"*\"]\n---\n# wf\n"

// When the active workflow declares create_review, a structurally-valid draft is
// judged by that reviewer; its reject blocks creation with the notes.
func TestReviewCreateContentReject(t *testing.T) {
	g, _ := gater(t, `{"decision":"reject","notes":"ACs do not match the goal"}`,
		fakeDocs{workflow: createWF, skillBody: "content/alignment rubric", skillFound: true})
	dec, err := g.ReviewCreate(context.Background(), validDraft)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Gated || dec.Accept {
		t.Fatalf("want gated reject from content review, got %+v", dec)
	}
	if dec.Skill != "my-create-review" || dec.Notes == "" {
		t.Errorf("want reject by the workflow-declared skill with notes, got %+v", dec)
	}
}

// The declared reviewer's accept persists (structure + content both pass).
func TestReviewCreateContentAccept(t *testing.T) {
	g, _ := gater(t, `{"decision":"accept","notes":"aligned"}`,
		fakeDocs{workflow: createWF, skillBody: "content/alignment rubric", skillFound: true})
	dec, err := g.ReviewCreate(context.Background(), validDraft)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Gated || !dec.Accept || dec.Skill != "my-create-review" {
		t.Fatalf("want gated accept by the workflow-declared skill, got %+v", dec)
	}
}

// With NO create_review declared, creation is deterministic-only: the content
// reviewer (an agent) is never run.
func TestReviewCreateNoWorkflowBinding(t *testing.T) {
	g, r := gater(t, `{"decision":"reject","notes":"should not run"}`,
		fakeDocs{workflow: plainWF, skillFound: false})
	dec, err := g.ReviewCreate(context.Background(), validDraft)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Gated || !dec.Accept || dec.Skill != structureSkill {
		t.Fatalf("no create_review → deterministic accept, got %+v", dec)
	}
	if r.got.SystemPrompt != "" {
		t.Error("no content reviewer should run when the workflow declares none")
	}
}

// A structural failure pre-empts: the content reviewer is never run on a
// malformed draft, even when the workflow declares one.
func TestReviewCreateStructurePreemptsContent(t *testing.T) {
	g, r := gater(t, `{"decision":"accept"}`,
		fakeDocs{workflow: createWF, skillBody: "content/alignment rubric", skillFound: true})
	bad := verb.CreateDraft{Kind: "story", Title: "x"} // no goal, no numbered AC
	dec, err := g.ReviewCreate(context.Background(), bad)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Gated || dec.Accept || dec.Skill != structureSkill {
		t.Fatalf("structural failure must pre-empt with a %s reject, got %+v", structureSkill, dec)
	}
	if r.got.SystemPrompt != "" {
		t.Error("the content reviewer must NOT run when the structural check fails")
	}
}

// stepWF declares a step-summary node; stepWFOptional declares a non-mandatory
// one; the bare baselineWorkflow body (testWorkflow) declares none.
const stepWF = "---\nname: " + baselineWorkflow + "\ntype: workflow\n---\n" + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  step        [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  backlog -> in_progress -> done
}
` + "```"

const stepWFOptional = "---\nname: " + baselineWorkflow + "\ntype: workflow\n---\n" + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  step        [agent=reviewer, prompt="@skill:satelle-step-summary"]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  backlog -> in_progress -> done
}
` + "```"

func TestSummariseReturnsTrimmedProse(t *testing.T) {
	g, r := gater(t, "  Moved from in_progress to done after the criteria were met.\n",
		fakeDocs{workflow: stepWF, skillBody: "summariser rubric", skillFound: true})
	s, err := g.Summarise(context.Background(), workitem.Item{Status: "in_progress"}, "in_progress", "done")
	if err != nil {
		t.Fatal(err)
	}
	if s != "Moved from in_progress to done after the criteria were met." {
		t.Errorf("summary = %q", s)
	}
	if r.got.SystemPrompt != "summariser rubric" {
		t.Errorf("summariser rubric should be the system prompt, got %q", r.got.SystemPrompt)
	}
	// Read-only grant (sty_659848ad) — the default is exactly Read,Grep,Glob: no
	// mutators, and no shell at all (the reviewer reads materialised substrate).
	if r.got.AllowedTools != "Read,Grep,Glob" {
		t.Errorf("default reviewer grant = %q, want read-only Read,Grep,Glob", r.got.AllowedTools)
	}
	for _, banned := range []string{"Write", "Edit", "NotebookEdit", "Bash"} {
		if contains(r.got.AllowedTools, banned) {
			t.Errorf("tool grant %q must not include %q", r.got.AllowedTools, banned)
		}
	}
}

// When the active workflow declares NO step node, the summariser does not run —
// transparent opt-in (sty_9a139c78).
func TestSummariseSkippedWhenNotDeclared(t *testing.T) {
	g, r := gater(t, "should not run", fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	s, err := g.Summarise(context.Background(), workitem.Item{Status: "in_progress"}, "in_progress", "done")
	if err != nil {
		t.Fatal(err)
	}
	if s != "" {
		t.Errorf("no step node declared → no summary, got %q", s)
	}
	if r.got.SystemPrompt != "" {
		t.Errorf("summariser must not run when the workflow declares no step node")
	}
}

// A mandatory step node whose rubric is absent surfaces an error (the gap is not
// silently swallowed); a non-mandatory one stays best-effort (empty, no error).
func TestSummariseMandatoryVsOptionalWhenAbsent(t *testing.T) {
	g, _ := gater(t, "", fakeDocs{workflow: stepWF, skillFound: false}) // mandatory
	if _, err := g.Summarise(context.Background(), workitem.Item{Status: "in_progress"}, "in_progress", "done"); err == nil {
		t.Error("mandatory step summary with an absent rubric should error")
	}
	g2, _ := gater(t, "", fakeDocs{workflow: stepWFOptional, skillFound: false}) // optional
	if s, err := g2.Summarise(context.Background(), workitem.Item{Status: "in_progress"}, "in_progress", "done"); err != nil || s != "" {
		t.Errorf("optional step summary with an absent rubric should be empty/no-error, got %q/%v", s, err)
	}
}

func TestSummariseEmptyWhenRubricAbsent(t *testing.T) {
	g, r := gater(t, "should not run", fakeDocs{workflow: stepWFOptional, skillFound: false})
	s, err := g.Summarise(context.Background(), workitem.Item{Status: "in_progress"}, "in_progress", "done")
	if err != nil {
		t.Fatal(err)
	}
	if s != "" {
		t.Errorf("want empty summary when rubric absent, got %q", s)
	}
	if r.got.SystemPrompt != "" {
		t.Error("summariser must not run without a rubric")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestParseDecisionStrict(t *testing.T) {
	for _, in := range []string{`{"decision":"maybe"}`, `{"notes":"x"}`, ``, `no json`} {
		if _, err := parseDecision([]byte(in)); err == nil {
			t.Errorf("parseDecision(%q) should error", in)
		}
	}
}

func TestParseDecisionLenient(t *testing.T) {
	cases := []struct {
		in     string
		accept bool
		notes  string
	}{
		// prose around the verdict
		{`I judged it. {"decision":"accept","notes":""} Done.`, true, ""},
		// wrapping braces — the brittle case dogfooding hit
		{`{{"decision":"reject","notes":"add criteria"}}`, false, "add criteria"},
		// a code-fenced example before the real verdict
		{"```json\n{\"decision\": \"accept\"}\n```\nFinal: {\"decision\":\"reject\",\"notes\":\"no\"}", false, "no"},
		// a brace inside the notes string must not unbalance extraction
		{`{"decision":"reject","notes":"missing the {foo} block"}`, false, "missing the {foo} block"},
	}
	for _, c := range cases {
		dec, err := parseDecision([]byte(c.in))
		if err != nil {
			t.Errorf("parseDecision(%q): %v", c.in, err)
			continue
		}
		if dec.Accept != c.accept || dec.Notes != c.notes {
			t.Errorf("parseDecision(%q) = {accept:%v notes:%q}, want {accept:%v notes:%q}", c.in, dec.Accept, dec.Notes, c.accept, c.notes)
		}
	}
}

// webWorkflow is a category-specific workflow (applies_to: ["web"]) whose
// in_progress→done edge names a different reviewer than the baseline.
const webWorkflow = `---
name: satelle-web-workflow
applies_to: ["web"]
---
transitions:
  - {from: in_progress, to: done, reviewer_skill: "satelle-web-done-review"}
`

func TestActiveWorkflowSelectByCategory(t *testing.T) {
	docs := fakeDocs{
		workflow:   testWorkflow, // baseline, applies_to absent → wildcard fallback via Get
		skillBody:  "rubric body",
		skillFound: true,
		extraWorkflows: []docindex.Doc{
			{Kind: "workflows", Name: "satelle-web-workflow", Body: webWorkflow},
		},
	}
	cases := []struct {
		name     string
		category string
		want     string
	}{
		{"specific category match wins", "web", "satelle-web-done-review"},
		{"no match falls back to baseline", "infra", "satelle-story-done-review"},
		{"empty category uses baseline", "", "satelle-story-done-review"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := gater(t, `{"decision":"accept","notes":""}`, docs)
			dec, err := g.Gate(context.Background(),
				workitem.Item{ID: "sty_1", Status: "in_progress", Category: tc.category}, "done")
			if err != nil {
				t.Fatal(err)
			}
			if dec.Skill != tc.want {
				t.Errorf("category %q → skill %q, want %q", tc.category, dec.Skill, tc.want)
			}
		})
	}
}

func TestFrontmatterListForms(t *testing.T) {
	inline := frontmatterList("---\napplies_to: [\"*\", web]\n---\nx", "applies_to")
	if len(inline) != 2 || inline[0] != "*" || inline[1] != "web" {
		t.Fatalf("inline: %v", inline)
	}
	block := frontmatterList("---\nname: w\napplies_to:\n  - web\n  - infra\nother: y\n---\nx", "applies_to")
	if len(block) != 2 || block[1] != "infra" {
		t.Fatalf("block: %v", block)
	}
	if frontmatterList("no frontmatter", "applies_to") != nil {
		t.Fatalf("want nil without frontmatter")
	}
}

const checkSkill = "---\nname: satelle-story-integration-review\nkind: skill\ncheck: \"run-the-suite\"\n---\n# Integration gate\nRuns the suite.\n"

func TestFunctionalCheckGate(t *testing.T) {
	// A workflow edge whose reviewer skill carries a `check:` runs deterministically.
	wf := "transitions:\n  - {from: in_progress, to: integrated, reviewer_skill: \"satelle-story-integration-review\"}\n"

	t.Run("pass accepts, agent not run", func(t *testing.T) {
		g, r := gater(t, `{"decision":"reject"}`, fakeDocs{workflow: wf, skillBody: checkSkill, skillFound: true})
		var ran string
		g.check = func(_ context.Context, dir, command string) (string, error) { ran = command; return "ok\n", nil }
		dec, err := g.Gate(context.Background(), workitem.Item{Status: "in_progress"}, "integrated")
		if err != nil {
			t.Fatal(err)
		}
		if !dec.Gated || !dec.Accept {
			t.Fatalf("want gated accept, got %+v", dec)
		}
		if ran != "run-the-suite" {
			t.Errorf("check command = %q, want run-the-suite", ran)
		}
		if r.got.SystemPrompt != "" {
			t.Errorf("LLM reviewer must not run for a functional-check gate")
		}
	})

	t.Run("fail rejects with output tail", func(t *testing.T) {
		g, _ := gater(t, ``, fakeDocs{workflow: wf, skillBody: checkSkill, skillFound: true})
		g.check = func(_ context.Context, dir, command string) (string, error) {
			return "FAIL tests\n2 failures\n", errFakeExit
		}
		dec, err := g.Gate(context.Background(), workitem.Item{Status: "in_progress"}, "integrated")
		if err != nil {
			t.Fatal(err)
		}
		if !dec.Gated || dec.Accept {
			t.Fatalf("want gated reject, got %+v", dec)
		}
		if !strings.Contains(dec.Notes, "2 failures") {
			t.Errorf("reject notes should carry the check output tail, got %q", dec.Notes)
		}
	})
}

var errFakeExit = errFake("exit status 1")

type errFake string

func (e errFake) Error() string { return string(e) }

func TestBodyCheckBlock_selfContained(t *testing.T) {
	// A skill carrying an embedded ```check block is self-contained — no external
	// file. skillCheck must extract the block (preferred over a frontmatter check).
	body := "---\nname: x\ncheck: \"frontmatter-fallback\"\n---\n# Gate\n\n```check\n#!/usr/bin/env bash\nset -e\necho hello\n```\n\ntrailing prose\n"
	got := skillCheck(body)
	if got == "frontmatter-fallback" {
		t.Fatal("body ```check block should win over frontmatter check:")
	}
	if !strings.Contains(got, "echo hello") || strings.Contains(got, "```") {
		t.Fatalf("extracted block wrong: %q", got)
	}
	// No block → frontmatter fallback still works.
	if skillCheck("---\ncheck: \"only-frontmatter\"\n---\nbody") != "only-frontmatter" {
		t.Fatal("frontmatter check: fallback broken")
	}
}

func TestOrderedWorkflowsPriority(t *testing.T) {
	sysWild := docindex.Doc{Name: "satelle-baseline-workflow", Embedded: true,
		Body: "---\nscope: system\napplies_to: [\"*\"]\n---\n"}
	repoWild := docindex.Doc{Name: "satelle-workflow", Embedded: false,
		Body: "---\nscope: project\napplies_to: [\"*\"]\n---\n"}
	repoSpec := docindex.Doc{Name: "satelle-web-workflow", Embedded: false,
		Body: "---\nscope: project\napplies_to: [\"web\"]\n---\n"}
	all := []docindex.Doc{sysWild, repoWild, repoSpec}

	// No category: project wildcard beats the embedded system default.
	got := OrderedWorkflows(all, "")
	if len(got) != 2 || got[0].Name != "satelle-workflow" || got[1].Name != "satelle-baseline-workflow" {
		t.Fatalf("wildcard order = %v, want [satelle-workflow, satelle-baseline-workflow]", names(got))
	}
	// Category 'web': the category-specific repo workflow leads.
	got = OrderedWorkflows(all, "web")
	if got[0].Name != "satelle-web-workflow" {
		t.Fatalf("category-web head = %s, want satelle-web-workflow", got[0].Name)
	}
}

// TestExecutionResolvesToTaskExecutionWorkflow asserts the kind-aware resolution
// (sty_ef08ce2a): an execution resolves by its KIND ("execution") to a workflow
// declaring applies_to:["execution"], and NEVER falls through to the wildcard
// story workflow. A story still resolves by category to the wildcard.
func TestExecutionResolvesToTaskExecutionWorkflow(t *testing.T) {
	storyWild := docindex.Doc{Name: "satelle-project-workflow", Embedded: false,
		Body: "---\nscope: project\napplies_to: [\"*\"]\n---\n"}
	taskExec := docindex.Doc{Name: "satelle-task-workflow", Embedded: false,
		Body: "---\nscope: project\napplies_to: [\"execution\"]\n---\n"}
	all := []docindex.Doc{storyWild, taskExec}

	// The resolution key for an execution is its kind, not its (empty) category.
	if got := workflowCategory(workitem.Item{Kind: workitem.KindExecution}); got != "execution" {
		t.Fatalf("workflowCategory(execution) = %q, want \"execution\"", got)
	}
	// An execution resolves to the task-execution workflow, not the story wildcard.
	got := OrderedWorkflows(all, workflowCategory(workitem.Item{Kind: workitem.KindExecution}))
	if len(got) == 0 || got[0].Name != "satelle-task-workflow" {
		t.Fatalf("execution head = %v, want satelle-task-workflow (not the story workflow)", names(got))
	}
	// A story keeps resolving by category to the wildcard project workflow.
	sk := OrderedWorkflows(all, workflowCategory(workitem.Item{Kind: workitem.KindStory, Category: "feature"}))
	if len(sk) == 0 || sk[0].Name != "satelle-project-workflow" {
		t.Fatalf("story head = %v, want satelle-project-workflow", names(sk))
	}
}

// TestOrderedWorkflowsParentCategories asserts a category-specific repo workflow
// listing several categories (applies_to ["epic-parent","parent"]) leads for EACH
// of them, overriding the project wildcard — the selection that makes
// satelle-parent-workflow the active lifecycle for container stories.
func TestOrderedWorkflowsParentCategories(t *testing.T) {
	repoWild := docindex.Doc{Name: "satelle-project-workflow", Embedded: false,
		Body: "---\nscope: project\napplies_to: [\"*\"]\n---\n"}
	repoParent := docindex.Doc{Name: "satelle-parent-workflow", Embedded: false,
		Body: "---\nscope: project\napplies_to: [\"epic-parent\", \"parent\"]\n---\n"}
	all := []docindex.Doc{repoWild, repoParent}

	for _, cat := range []string{"epic-parent", "parent"} {
		got := OrderedWorkflows(all, cat)
		if len(got) == 0 || got[0].Name != "satelle-parent-workflow" {
			t.Errorf("category %q head = %v, want satelle-parent-workflow first", cat, names(got))
		}
	}
	// A non-container category still resolves to the wildcard project workflow.
	if got := OrderedWorkflows(all, "feature"); len(got) == 0 || got[0].Name != "satelle-project-workflow" {
		t.Errorf("category feature head = %v, want satelle-project-workflow", names(got))
	}
}

func names(ds []docindex.Doc) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Name
	}
	return out
}

const dotWF = `---
name: x
---
# w

` + "```dot" + `
digraph w {
  in_progress [agent=executor]
  committed   [agent=reviewer, prompt="@skill:satelle-commit-push-reviewer"]
  in_progress -> committed -> done
}
` + "```" + `
`

func TestReviewerSkillsForDOT(t *testing.T) {
	skills, declared := reviewerSkillsFor(dotWF, "in_progress", "committed")
	if !declared || len(skills) != 1 || skills[0] != "satelle-commit-push-reviewer" {
		t.Fatalf("in_progress->committed: skills=%v declared=%v", skills, declared)
	}
	if _, declared := reviewerSkillsFor(dotWF, "in_progress", "nope"); declared {
		t.Errorf("an undeclared edge should report declared=false")
	}
	if skills, declared := reviewerSkillsFor(dotWF, "committed", "done"); !declared || len(skills) != 0 {
		t.Errorf("committed->done should be declared and ungated: skills=%v declared=%v", skills, declared)
	}
}

// A container close gate is judged from the children SATELLE injects into the
// payload (resolved from the DB), not any on-disk story mirror (sty_fa1e02e1).
func TestGatePayloadIncludesChildren(t *testing.T) {
	g, r := gater(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	g.SetChildrenResolver(func(_ context.Context, parentID string) []ChildState {
		if parentID != "sty_parent" {
			t.Errorf("resolver called with %q, want sty_parent", parentID)
		}
		return []ChildState{{ID: "sty_child1", Status: "done"}, {ID: "sty_child2", Status: "in_progress"}}
	})
	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_parent", Status: "in_progress", Category: "epic-parent"}, "done"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.got.Payload, `"children"`) || !strings.Contains(r.got.Payload, "sty_child2") {
		t.Errorf("close-gate payload must carry the children:\n%s", r.got.Payload)
	}
}

func TestSetReviewerModel(t *testing.T) {
	g := New(nil, nil, "", "")
	if g.model != "" {
		t.Fatalf("default model = %q, want empty (inherits the agent CLI default)", g.model)
	}
	g.SetReviewerModel("sonnet")
	if g.model != "sonnet" {
		t.Errorf("after override model = %q, want sonnet", g.model)
	}
	g.SetReviewerModel("")
	if g.model != "sonnet" {
		t.Errorf("empty override should be a no-op, model = %q", g.model)
	}
}

// The model set on the binding must reach the runner Request (so the harness
// --model carries it to the reviewer subprocess).
func TestReviewerModelReachesRunner(t *testing.T) {
	g, r := gater(t, "  recap.\n", fakeDocs{workflow: stepWF, skillBody: "rubric", skillFound: true})
	g.SetReviewerModel("sonnet")
	if _, err := g.Summarise(context.Background(), workitem.Item{Status: "in_progress"}, "in_progress", "done"); err != nil {
		t.Fatal(err)
	}
	if r.got.Model != "sonnet" {
		t.Errorf("runner Request model = %q, want sonnet", r.got.Model)
	}
}

func TestSetReviewerTools(t *testing.T) {
	g := New(nil, nil, "", "")
	if g.tools != defaultTools {
		t.Fatalf("default tools = %q, want %q", g.tools, defaultTools)
	}
	g.SetReviewerTools("Read,Edit,Write")
	if g.tools != "Read,Edit,Write" {
		t.Errorf("after override tools = %q, want Read,Edit,Write", g.tools)
	}
	g.SetReviewerTools("")
	if g.tools != "Read,Edit,Write" {
		t.Errorf("empty override should be a no-op, tools = %q", g.tools)
	}
}

func TestSetRunner(t *testing.T) {
	g := New(nil, nil, "", "")
	r, _ := agentcli.NewRunner("codex")
	g.SetRunner(r)
	if g.runner == nil || g.runner.Name() != "codex" {
		t.Fatalf("SetRunner should override the runner, got %v", g.runner)
	}
	g.SetRunner(nil) // nil is ignored
	if g.runner == nil || g.runner.Name() != "codex" {
		t.Errorf("a nil runner must be ignored")
	}
}

// TestStampedWorkflowName reads the workflow:<name> stamp from an item's tags.
func TestStampedWorkflowName(t *testing.T) {
	if got := stampedWorkflowName(workitem.Item{Tags: []string{"a", "workflow:my-wf", "b"}}); got != "my-wf" {
		t.Errorf("stampedWorkflowName = %q, want my-wf", got)
	}
	if got := stampedWorkflowName(workitem.Item{Tags: []string{"a", "b"}}); got != "" {
		t.Errorf("un-stamped item = %q, want empty", got)
	}
}

// TestActiveWorkflowPreferringStampWins: the STAMPED workflow governs the story,
// overriding category selection; an un-stamped item resolves by category; a stamp
// that no longer resolves falls back to category (sty_3800ac23).
func TestActiveWorkflowPreferringStampWins(t *testing.T) {
	wfFeature := docindex.Doc{Kind: "workflows", Name: "wf-feature",
		Body: "---\nname: wf-feature\ntype: workflow\napplies_to: [\"feature\"]\n---\n# f\n"}
	wfChore := docindex.Doc{Kind: "workflows", Name: "wf-chore",
		Body: "---\nname: wf-chore\ntype: workflow\napplies_to: [\"chore\"]\n---\n# c\n"}
	g, _ := gater(t, "", fakeDocs{workflow: plainWF, extraWorkflows: []docindex.Doc{wfFeature, wfChore}})
	ctx := context.Background()

	// Category "feature" alone selects wf-feature.
	if doc, err := g.activeWorkflowPreferring(ctx, "feature", ""); err != nil || doc.Name != "wf-feature" {
		t.Fatalf("category feature → %q,%v; want wf-feature", doc.Name, err)
	}
	// A stamp for wf-chore WINS even though the category is feature.
	if doc, err := g.activeWorkflowPreferring(ctx, "feature", "wf-chore"); err != nil || doc.Name != "wf-chore" {
		t.Fatalf("stamped wf-chore → %q,%v; want wf-chore (stamp wins over category)", doc.Name, err)
	}
	// A stamp that no longer resolves falls back to category selection.
	if doc, err := g.activeWorkflowPreferring(ctx, "feature", "gone"); err != nil || doc.Name != "wf-feature" {
		t.Fatalf("stale stamp → %q,%v; want wf-feature (fallback)", doc.Name, err)
	}
}

// TestWorkflowConsistency: two REPO workflows claiming the same wildcard is
// flagged (over-configuration); an unresolved referenced skill is flagged; a
// clean set and embedded-only ties are not (sty_4c0c7246).
func TestWorkflowConsistency(t *testing.T) {
	repoWild := func(name string) docindex.Doc {
		return docindex.Doc{Name: name, Embedded: false,
			Body: "---\nname: " + name + "\ntype: workflow\napplies_to: [\"*\"]\n---\n# w\n"}
	}
	// (1) Two repo wildcards → ambiguity flagged.
	probs := WorkflowConsistency([]docindex.Doc{repoWild("a"), repoWild("b")}, func(string) bool { return true })
	if len(probs) == 0 || !strings.Contains(strings.Join(probs, "\n"), "same precedence") {
		t.Errorf("two repo wildcards should be flagged ambiguous, got %v", probs)
	}
	// Embedded ties are NOT flagged (the canonical defaults are the single source).
	emb := func(name string) docindex.Doc {
		return docindex.Doc{Name: name, Embedded: true,
			Body: "---\nname: " + name + "\ntype: workflow\napplies_to: [\"*\"]\n---\n# w\n"}
	}
	if p := WorkflowConsistency([]docindex.Doc{emb("e1"), emb("e2")}, nil); len(p) != 0 {
		t.Errorf("embedded ties must not be flagged, got %v", p)
	}
	// (2) An unresolved referenced skill is flagged; resolved → clean.
	wfSkill := docindex.Doc{Name: "x", Embedded: false,
		Body: "---\nname: x\ntype: workflow\napplies_to: [\"feature\"]\n---\n" +
			"```dot\ndigraph x {\n  backlog -> in_progress [reviewer_skill=\"missing-skill\"]\n}\n```\n"}
	miss := WorkflowConsistency([]docindex.Doc{wfSkill}, func(s string) bool { return s != "missing-skill" })
	if len(miss) == 0 || !strings.Contains(strings.Join(miss, "\n"), "missing-skill") {
		t.Errorf("unresolved referenced skill should be flagged, got %v", miss)
	}
	if ok := WorkflowConsistency([]docindex.Doc{wfSkill}, func(string) bool { return true }); len(ok) != 0 {
		t.Errorf("a resolved referenced skill is clean, got %v", ok)
	}
}
