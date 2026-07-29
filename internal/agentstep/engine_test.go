package agentstep

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/logfile"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// wfDoc wraps a digraph in the conformant workflow-doc envelope (frontmatter +
// fenced ```dot) a Gate-path fixture needs: the gate's structure guard refuses a
// governing workflow that fails its deterministic check (sty_d0d6bb67), so any
// workflow a test drives Gate under must be a well-formed doc, exactly like the
// authored substrate.
func wfDoc(name, appliesTo, graph string) string {
	return "---\nname: " + name + "\ntype: workflow\ndescription: test lifecycle\napplies_to: [" + appliesTo +
		"]\nscope: system\n---\n\n" + "```dot\n" + graph + "\n```\n"
}

// conformantSkill wraps a bare rubric in the conformant skill-doc envelope for
// the same reason (the gate refuses an invalid PRESENT reviewer skill); a body
// already carrying frontmatter is returned as-is so fixtures with authored
// frontmatter (e.g. a check: skill) stay verbatim. The envelope includes a
// minimal verdict-contract phrase so the reviewer skill contract check (design
// §6.3 / sty_e21cbc08) passes for test stubs that only supply a short rubric.
func conformantSkill(name, rubric string) string {
	if strings.HasPrefix(rubric, "---\n") {
		return rubric
	}
	return "---\nname: " + name + "\ntype: skill\ndescription: test rubric\n---\n\n" +
		rubric + "\n\nReturn JSON {\"decision\": \"accept\"|\"reject\", \"notes\": \"…\"}.\n"
}

var testWorkflow = wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog -> in_progress [reviewer_skill="satelle-story-intent-review"]
  in_progress -> done [reviewer_skill="satelle-story-done-review"]
  backlog -> cancelled
}`)

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
			return docindex.Doc{Kind: kind, Name: name, Body: conformantSkill(name, d.skillBody)}, nil
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
	g, r := newEngine(t, `{"decision":"accept"}`, docs)
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
	g, r := newEngine(t, `{"decision":"accept"}`, docs)
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
	return docindex.Doc{Kind: "skills", Name: name, Body: conformantSkill(name, "rubric body")}
}

// engageDOT is a valid DOT workflow whose start state is backlog. Its path to done
// runs through an executor step (commit_push) with an @skill: prompt — the thing
// the engagement guard resolves.
var engageDOT = wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  commit_push [agent=executor, prompt="@skill:commit-push"]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  backlog -> in_progress [reviewer_skill="satelle-story-intent-review"]
  in_progress -> commit_push
  commit_push -> done
  backlog -> cancelled
}`)

// TestEngagementBlockedWhenExecutorSkillMissing: engaging under a workflow whose
// path to done has an executor step with an unresolvable skill is rejected up
// front (deterministically, no agent), naming the missing skill.
func TestEngagementBlockedWhenExecutorSkillMissing(t *testing.T) {
	// Only the intent + done reviewers resolve; the executor skill commit-push does NOT.
	docs := fakeDocs{workflow: engageDOT, extraSkills: []docindex.Doc{
		skillDoc("satelle-story-intent-review"),
		skillDoc("satelle-story-done-review"),
	}}
	g, _ := newEngine(t, `{"decision":"accept"}`, docs)
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

// codeSkillDOT is a workflow whose in_progress executor names @skill:code —
// the post-removal scenario where the repo has no disk copy either (sty_01f49dd5 AC5).
var codeSkillDOT = wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  backlog -> in_progress [reviewer_skill="satelle-story-intent-review"]
  in_progress -> done
  backlog -> cancelled
}`)

// TestEngagementBlockedWhenCodeSkillMissing (sty_01f49dd5 AC5): a workflow that
// still references @skill:code with no disk and no embed copy degrades with a
// clear missing-skill message naming `code`, not a crash.
func TestEngagementBlockedWhenCodeSkillMissing(t *testing.T) {
	docs := fakeDocs{workflow: codeSkillDOT, extraSkills: []docindex.Doc{
		skillDoc("satelle-story-intent-review"),
		skillDoc("satelle-story-done-review"),
	}}
	g, _ := newEngine(t, `{"decision":"accept"}`, docs)
	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if dec.Accept {
		t.Fatal("expected engagement blocked (code skill missing), got accept")
	}
	if dec.Skill != "satelle-workflow-skill-check" {
		t.Errorf("blocking skill = %q, want satelle-workflow-skill-check", dec.Skill)
	}
	if !strings.Contains(dec.Notes, "code") {
		t.Errorf("reject notes should name the missing skill code: %q", dec.Notes)
	}
}

// TestEngagementProceedsWhenExecutorSkillsResolve: when every executor skill on
// the path to done resolves, the guard passes and the edge proceeds normally.
func TestEngagementProceedsWhenExecutorSkillsResolve(t *testing.T) {
	docs := fakeDocs{workflow: engageDOT, skillBody: "rubric", skillFound: true}
	g, _ := newEngine(t, `{"decision":"accept"}`, docs)
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
	g, _ := newEngine(t, `{"decision":"accept"}`, docs)
	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "commit_push")
	if err != nil {
		t.Fatal(err)
	}
	if dec.Skill == "satelle-workflow-skill-check" {
		t.Errorf("executor guard must not run off the engagement edge; skill=%q", dec.Skill)
	}
}

// Surface-scoped design gate for skip-telemetry tests (sty_dcce86d5 AC4).
var scopedAppliesDOT = wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  design [agent=reviewer, prompt="@skill:design-review", on="in_progress", applies_to="surface:ui"]
  estimate [agent=reviewer, prompt="@skill:satelle-estimate-actual-review", on="in_progress"]
  backlog -> in_progress [reviewer_skill="satelle-story-intent-review"]
  in_progress -> done
}`)

// TestScopedGateSkippedTelemetry: applies_to filter emits scoped-gate-skipped
// when tags miss, and does not when tags match (sty_dcce86d5 AC4).
func TestScopedGateSkippedTelemetry(t *testing.T) {
	docs := fakeDocs{workflow: scopedAppliesDOT, skillBody: "rubric", skillFound: true}
	g, _ := newEngine(t, `{"decision":"accept"}`, docs)
	recs := captureTelemetry(g)

	// Miss: surface:cli should skip design-review
	_, err := g.Gate(context.Background(), workitem.Item{
		ID: "sty_cli", Status: "backlog", Tags: []string{"surface:cli"},
	}, "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range *recs {
		if r.kind == "scoped-gate-skipped" {
			found = true
			if r.data["skill"] != "design-review" {
				t.Errorf("skip skill = %v", r.data["skill"])
			}
			if r.data["reason"] != "applies_to" {
				t.Errorf("reason = %v", r.data["reason"])
			}
		}
	}
	if !found {
		t.Fatalf("expected scoped-gate-skipped telemetry, got %#v", *recs)
	}

	// Hit: surface:ui should not skip design
	*recs = nil
	_, err = g.Gate(context.Background(), workitem.Item{
		ID: "sty_ui", Status: "backlog", Tags: []string{"surface:ui"},
	}, "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range *recs {
		if r.kind == "scoped-gate-skipped" {
			t.Errorf("must not skip when tags match: %#v", r)
		}
	}
}

// Augmentation DOT: code-ui is only required for surface:ui (sty_8225d8a5 AC5).
var engageAugDOT = wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done        [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  codeui      [agent=executor, prompt="@skill:code-ui", on="in_progress", applies_to="surface:ui"]
  backlog -> in_progress [reviewer_skill="satelle-story-intent-review"]
  in_progress -> done
}`)

// TestEngagementAugmentationSurfaceAware: missing code-ui blocks surface:ui
// engagement and does NOT block surface:cli (sty_8225d8a5).
func TestEngagementAugmentationSurfaceAware(t *testing.T) {
	docs := fakeDocs{workflow: engageAugDOT, extraSkills: []docindex.Doc{
		skillDoc("satelle-story-intent-review"),
		skillDoc("satelle-story-done-review"),
		skillDoc("code"),
		// code-ui deliberately missing
	}}
	g, _ := newEngine(t, `{"decision":"accept"}`, docs)

	ui, err := g.Gate(context.Background(), workitem.Item{
		ID: "sty_ui", Status: "backlog", Tags: []string{"surface:ui"},
	}, "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if ui.Accept {
		t.Fatal("surface:ui must block when code-ui missing")
	}
	if !strings.Contains(ui.Notes, "code-ui") {
		t.Errorf("notes should name code-ui: %q", ui.Notes)
	}

	cli, err := g.Gate(context.Background(), workitem.Item{
		ID: "sty_cli", Status: "backlog", Tags: []string{"surface:cli"},
	}, "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if !cli.Accept {
		t.Fatalf("surface:cli must NOT require code-ui, got %+v", cli)
	}
}

func newEngine(t *testing.T, out string, docs fakeDocs) (*Engine, *fakeRunner) {
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

// summaryWorkflow declares a MANDATORY step-summary node, so Summarise runs.
var summaryWorkflow = wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog -> in_progress [reviewer_skill="satelle-story-intent-review"]
  in_progress -> done [reviewer_skill="satelle-story-done-review"]
  step [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]
  backlog -> cancelled
}`)

// TestSummarise_retriesTransientKillThenSucceeds pins AC1 (sty_a1151fb0): the
// summariser now retries the SAME transient a reviewer does (a killed/empty
// subprocess) instead of losing the summary on the first shot.
func TestSummarise_retriesTransientKillThenSucceeds(t *testing.T) {
	docs := fakeDocs{workflow: summaryWorkflow, skillBody: "summarise rubric", skillFound: true}
	r := &scriptedRunner{results: []struct {
		out string
		err error
	}{
		{err: errFakeAgent},     // signal: killed → transient
		{out: "   "},            // empty output → transient
		{out: "the step recap"}, // succeeds on the 3rd try
	}}
	g := New(r, docs, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }

	got, err := g.Summarise(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "in_progress", "done")
	if err != nil {
		t.Fatalf("summarise should succeed after a transient retry, got err: %v", err)
	}
	if got.Text != "the step recap" {
		t.Fatalf("want the recap once the summariser returns text, got %q", got.Text)
	}
	if r.calls != 3 {
		t.Fatalf("expected 3 attempts (2 transient + 1 success), got %d", r.calls)
	}
	// AC3 (sty_b73c3236): the summariser's OWN invocation (command/model) rides on
	// the result so the verb layer can fold its cost into an agent_invocation row —
	// closing the documented gap where its usage was previously discarded.
	if got.Command == "" {
		t.Error("summarise result should carry the resolved command (its own cost is no longer discarded)")
	}
}

// TestSummarise_failsFastOnDeadline: a deadline is a bound, not contention — the
// summariser must NOT retry it (one attempt), and for a mandatory node surfaces the
// gap as an error rather than looping a full window each try.
func TestSummarise_failsFastOnDeadline(t *testing.T) {
	docs := fakeDocs{workflow: summaryWorkflow, skillBody: "r", skillFound: true}
	r := &scriptedRunner{results: []struct {
		out string
		err error
	}{{err: context.DeadlineExceeded}}}
	g := New(r, docs, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }

	_, err := g.Summarise(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "in_progress", "done")
	if err == nil {
		t.Fatal("a deadline should surface an error for a mandatory summary")
	}
	if r.calls != 1 {
		t.Fatalf("a deadline must fail fast (no retry), got %d calls", r.calls)
	}
}

// TestMandatorySummary reports the workflow's mandatory-summary policy — the gate for
// the done-time missing-summary surfacing.
func TestMandatorySummary(t *testing.T) {
	g := New(&fakeRunner{}, fakeDocs{workflow: summaryWorkflow, skillFound: true}, "/repo", "")
	if !g.MandatorySummary(context.Background(), workitem.Item{ID: "sty_1"}) {
		t.Error("summaryWorkflow declares a mandatory step node — want true")
	}
	g2 := New(&fakeRunner{}, fakeDocs{workflow: testWorkflow, skillFound: true}, "/repo", "")
	if g2.MandatorySummary(context.Background(), workitem.Item{ID: "sty_1"}) {
		t.Error("testWorkflow declares no step node — want false")
	}
}

// TestParseProseDecision covers the prose-verdict fallback (sty_9485d47e): a
// reviewer that states its conclusion in prose (no JSON object) still yields a
// decision; ambiguous or marker-less output does not.
func TestParseProseDecision(t *testing.T) {
	cases := []struct {
		name       string
		out        string
		wantOK     bool
		wantAccept bool
	}{
		{"prose reject", "Verdict: **reject**. The sweep missed .claude/settings.json:6 — fix and resubmit.", true, false},
		{"prose accept", "All criteria verified in the tree.\n\nVerdict: accept", true, true},
		{"decision is form", "My decision is reject — AC2 unmet.", true, false},
		{"rubric echo is not a verdict", "Reject when a criterion is unmet; accept when all pass.", false, false},
		{"conflicting markers", "Verdict: accept. Wait — verdict: reject.", false, false},
		{"no marker", "rate limited, please retry", false, false},
		{"empty", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dec, ok := parseProseDecision([]byte(c.out))
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (dec %+v)", ok, c.wantOK, dec)
			}
			if ok && dec.Accept != c.wantAccept {
				t.Errorf("accept = %v, want %v", dec.Accept, c.wantAccept)
			}
			if ok && dec.Notes == "" {
				t.Error("prose verdict should carry the prose as notes")
			}
		})
	}
}

// A JSON decision object always wins over prose wording in the same output —
// parseDecision runs first and the fallback is never consulted (sty_9485d47e).
func TestGate_jsonVerdictPrecedesProse(t *testing.T) {
	g, _ := newEngine(t, `verdict: reject — but formally: {"decision":"accept","notes":"fine"}`,
		fakeDocs{workflow: testWorkflow, skillBody: "rubric body", skillFound: true})
	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "done")
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !dec.Accept {
		t.Fatalf("JSON accept must win over prose wording: %+v", dec)
	}
}

// A prose-only REJECT verdict blocks the transition immediately — one attempt,
// no transient retries — and its reasons reach the caller as notes (sty_9485d47e).
func TestGate_proseRejectBlocksWithReasons(t *testing.T) {
	r := &scriptedRunner{results: []struct {
		out string
		err error
	}{{out: "Verdict: **reject**. No reason recorded for cancelling — add one first."}}}
	g := New(r, fakeDocs{workflow: testWorkflow, skillBody: "rubric body", skillFound: true}, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }

	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "done")
	if err != nil {
		t.Fatalf("a prose verdict is a decision, not an error: %v", err)
	}
	if dec.Accept {
		t.Fatalf("prose reject must block: %+v", dec)
	}
	if !strings.Contains(dec.Notes, "No reason recorded") {
		t.Errorf("the prose reasons must reach the caller as notes, got: %q", dec.Notes)
	}
	if r.calls != 1 {
		t.Errorf("a prose verdict must not consume retries; got %d attempts", r.calls)
	}
}

// A prose-only ACCEPT verdict advances the gate on the first attempt (sty_9485d47e).
func TestGate_proseAcceptAdvances(t *testing.T) {
	r := &scriptedRunner{results: []struct {
		out string
		err error
	}{{out: "Everything checks out.\nVerdict: accept."}}}
	g := New(r, fakeDocs{workflow: testWorkflow, skillBody: "rubric body", skillFound: true}, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }

	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "done")
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !dec.Accept || r.calls != 1 {
		t.Fatalf("prose accept should advance on the first attempt: accept=%v calls=%d", dec.Accept, r.calls)
	}
}

// On a genuine no-verdict, reviewer.log captures the subprocess's FULL output
// (not a 300-char tail) and the surfaced error names where to find it; a prose
// fallback writes its own observability record (sty_9485d47e).
func TestReviewerLog_fullOutputAndProseFallback(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 400) + " the real reason is at the very end"
	r := &scriptedRunner{results: []struct {
		out string
		err error
	}{{out: "garbled " + long}}}
	g := New(r, fakeDocs{workflow: testWorkflow, skillBody: "rubric body", skillFound: true}, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }
	g.SetLogDir(dir, logfile.Config{})

	_, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "done")
	if err == nil {
		t.Fatal("expected a no-verdict error")
	}
	if !strings.Contains(err.Error(), "reviewer.log") {
		t.Errorf("error should name where the full output is logged: %v", err)
	}
	logBytes, rerr := os.ReadFile(filepath.Join(dir, "reviewer.log"))
	if rerr != nil {
		t.Fatalf("reviewer.log not written: %v", rerr)
	}
	if !strings.Contains(string(logBytes), "the real reason is at the very end") {
		t.Error("reviewer.log must carry the FULL output, not a truncated tail")
	}
	if !strings.Contains(string(logBytes), "no verdict in reviewer output") {
		t.Errorf("no-verdict-with-output should be labelled as such:\n%s", logBytes)
	}

	// A prose verdict writes a fallback record instead of a failure line.
	r2 := &scriptedRunner{results: []struct {
		out string
		err error
	}{{out: "Verdict: accept."}}}
	g2 := New(r2, fakeDocs{workflow: testWorkflow, skillBody: "rubric body", skillFound: true}, "/repo", "")
	g2.SetLogDir(dir, logfile.Config{})
	if _, err := g2.Gate(context.Background(), workitem.Item{ID: "sty_2", Status: "in_progress"}, "done"); err != nil {
		t.Fatalf("prose accept gate: %v", err)
	}
	logBytes, _ = os.ReadFile(filepath.Join(dir, "reviewer.log"))
	if !strings.Contains(string(logBytes), "prose-verdict fallback") {
		t.Errorf("prose fallback should be recorded for observability:\n%s", logBytes)
	}
}

// blockingRunner blocks until its context is done — a wedged nested agent.
type blockingRunner struct{ calls int }

func (b *blockingRunner) Name() string    { return "blocking" }
func (b *blockingRunner) Command() string { return "blocking" }
func (b *blockingRunner) Run(ctx context.Context, _ agentcli.Request) ([]byte, error) {
	b.calls++
	<-ctx.Done()
	return nil, ctx.Err()
}

// A running gate emits progress so a slow-but-working reviewer is visibly
// distinct from a hang (sty_6c88ca10).
func TestGate_emitsProgress(t *testing.T) {
	g, _ := newEngine(t, `{"decision":"accept"}`,
		fakeDocs{workflow: testWorkflow, skillBody: "rubric body", skillFound: true})
	var msgs []string
	g.SetProgress(func(m string) { msgs = append(msgs, m) })
	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "done"); err != nil {
		t.Fatalf("gate: %v", err)
	}
	if len(msgs) == 0 || !strings.Contains(msgs[0], "running reviewer") {
		t.Errorf("expected a 'running reviewer …' progress line, got %v", msgs)
	}
}

// A wedged reviewer subprocess is BOUNDED by the per-invocation deadline: the
// gate fails fast with a legible timeout (no blind retries of another full
// window) and does not enact (sty_6c88ca10).
func TestGate_agentTimeoutBoundsAWedgedReviewer(t *testing.T) {
	r := &blockingRunner{}
	g := New(r, fakeDocs{workflow: testWorkflow, skillBody: "rubric body", skillFound: true}, "/repo", "")
	g.agentTimeout = 20 * time.Millisecond
	g.backoff = func(int) time.Duration { return 0 }

	start := time.Now()
	_, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "done")
	if err == nil {
		t.Fatal("a timed-out gate must surface an error, not enact")
	}
	if !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "NOT enacted") {
		t.Errorf("timeout error should be legible and name the non-enactment: %v", err)
	}
	if r.calls != 1 {
		t.Errorf("a deadline expiry must fail fast, not retry; got %d attempts", r.calls)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("gate was not bounded: took %v", elapsed)
	}
}

func TestGateAcceptEnacts(t *testing.T) {
	g, r := newEngine(t, `the story is ready {"decision":"accept","notes":"looks good"} done`,
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
	g, _ := newEngine(t, `{"decision":"reject","notes":"no acceptance criteria"}`,
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
	g, r := newEngine(t, `{"decision":"reject"}`,
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
	g, r := newEngine(t, `{"decision":"reject"}`,
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
	g, _ := newEngine(t, `no json here`,
		fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	if _, err := g.Gate(context.Background(), workitem.Item{Status: "in_progress"}, "done"); err == nil {
		t.Fatal("expected error on unparseable reviewer output")
	}
}

func TestReviewerSkillsFor(t *testing.T) {
	if got, _, _, declared := reviewerSkillsFor(testWorkflow, "in_progress", "done"); len(got) != 1 || got[0] != "satelle-story-done-review" || !declared {
		t.Errorf("in_progress→done = (%v, %v), want ([done-review], true)", got, declared)
	}
	if got, _, _, declared := reviewerSkillsFor(testWorkflow, "backlog", "cancelled"); len(got) != 0 || !declared {
		t.Errorf("declared ungated edge = (%v, %v), want (nil, true)", got, declared)
	}
	if got, _, _, declared := reviewerSkillsFor(testWorkflow, "backlog", "nowhere"); len(got) != 0 || declared {
		t.Errorf("undeclared edge = (%v, %v), want (nil, false)", got, declared)
	}
	// An ordered list: reviewer_skills takes precedence and preserves order.
	multi := "transitions:\n  - {from: deployed, to: done, reviewer_skills: [first-review, second-review]}\n"
	if got, _, _, declared := reviewerSkillsFor(multi, "deployed", "done"); len(got) != 2 || got[0] != "first-review" || got[1] != "second-review" || !declared {
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
	wf := wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog -> in_progress
  in_progress -> done [reviewer_skill="rev-a,rev-b,rev-c"]
}`)
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
	wf := wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog -> in_progress
  in_progress -> done [reviewer_skill="rev-a,rev-b,rev-c"]
}`)
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

// scopedDOT wraps a digraph body in the conformant workflow-doc envelope, so
// wfdot.Parse resolves it and the gate's structure guard passes it.
func scopedDOT(graph string) string {
	return wfDoc(baselineWorkflow, `"*"`, graph)
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

// TestGateRefusesBrokenWorkflowStructure: a governing workflow that fails its
// deterministic structure check must never gate work — the transition is
// refused with the problems (sty_d0d6bb67), instead of silently proceeding
// under a broken definition.
func TestGateRefusesBrokenWorkflowStructure(t *testing.T) {
	broken := "---\nname: " + baselineWorkflow + "\n---\n# no type/description/scope, no DOT\n"
	g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: broken, skillBody: "rubric", skillFound: true})
	_, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "in_progress")
	if err == nil {
		t.Fatal("want the gate refused under a structurally broken workflow")
	}
	for _, want := range []string{baselineWorkflow, "structure validation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should carry %q: %v", want, err)
		}
	}
	if r.got.SystemPrompt != "" {
		t.Error("no reviewer may run under a broken workflow definition")
	}
}

// TestGateRefusesBrokenReviewerSkill: a PRESENT reviewer skill that fails its
// structure check refuses the gate (an absent one stays advisory by design).
func TestGateRefusesBrokenReviewerSkill(t *testing.T) {
	docs := fakeDocs{workflow: testWorkflow, skillFound: true, extraSkills: []docindex.Doc{
		// present but broken: frontmatter carries no type/description.
		{Kind: "skills", Name: "satelle-story-done-review", Body: "---\nname: satelle-story-done-review\n---\nrubric\n"},
	}}
	g, r := newEngine(t, `{"decision":"accept"}`, docs)
	_, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "done")
	if err == nil {
		t.Fatal("want the gate refused under a structurally broken reviewer skill")
	}
	for _, want := range []string{"satelle-story-done-review", "structure validation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should carry %q: %v", want, err)
		}
	}
	if r.got.SystemPrompt != "" {
		t.Error("a broken reviewer skill must not run")
	}
}

// dispatchWF allocates the plan step to the NAMED agent "architect" with a
// rubric — the executor-dispatch fixture (sty_fd427546).
var dispatchWF = wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog [shape=Mdiamond]
  plan [agent=architect, prompt="@skill:architecture-alignment"]
  in_progress [agent=executor]
  done [shape=Msquare]
  backlog -> plan -> in_progress -> done
}`)

// TestDispatchExecutorRunsNamedBinding: entering a named-agent state spawns the
// binding's harness with the item payload, the node's rubric, and the binding's
// tools/model — nothing hardcoded (sty_fd427546).
func TestDispatchExecutorRunsNamedBinding(t *testing.T) {
	docs := fakeDocs{workflow: dispatchWF, skillBody: "alignment rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	r := &fakeRunner{out: "did the work"}
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name != "architect" {
			return config.AgentBinding{}, false
		}
		// A backlog-dispatched agent is realistically READ-ONLY (like the planner) —
		// a code-editing grant here would trip the edit-gate timing lock (a
		// code-writer dispatched from a non-performing state), covered by
		// TestDispatchExecutorCodeWriterFromNonPerformingRefused.
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Grep,Glob,Bash(satelle:*)", Model: "fable"}, true
	})
	g.newRunner = func(_iface, cmd string) (agentcli.Runner, error) {
		if cmd != "fake -p {system}" {
			t.Errorf("runner built from %q, want the binding's command", cmd)
		}
		return r, nil
	}
	res, err := g.DispatchExecutor(context.Background(),
		workitem.Item{ID: "sty_1", Title: "Align the stories", Status: "backlog"}, "plan")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Dispatched || res.Agent != "architect" || res.Skill != "architecture-alignment" {
		t.Fatalf("result = %+v, want dispatched by architect under architecture-alignment", res)
	}
	if r.got.AllowedTools != "Read,Grep,Glob,Bash(satelle:*)" || r.got.Model != "fable" {
		t.Errorf("binding grant not applied: tools=%q model=%q", r.got.AllowedTools, r.got.Model)
	}
	// The resolved model is carried on the result so the ledger can record WHICH
	// model ran the step — the audit signal for per-step model mixing (sty_5d48317b).
	if res.Model != "fable" {
		t.Errorf("resolved model not carried on the dispatch result: %q", res.Model)
	}
	if !strings.Contains(r.got.SystemPrompt, "alignment rubric") {
		t.Errorf("node rubric missing from the system prompt:\n%s", r.got.SystemPrompt)
	}
	if !strings.Contains(r.got.SystemPrompt, "Do NOT change the item's status") {
		t.Errorf("executor charter missing:\n%s", r.got.SystemPrompt)
	}
	if !strings.Contains(r.got.Payload, "Align the stories") {
		t.Errorf("item payload missing from stdin: %q", r.got.Payload)
	}
	// No reviewer-only framing may leak into a dispatched EXECUTOR's context
	// (sty_f4c1bd90): the read-only call-to-action is reviewer-specific — a
	// performing agent is not "an isolated satelle reviewer" and is not told it
	// cannot modify the repository.
	for _, banned := range []string{"isolated satelle reviewer", "You judge only", "return your verdict"} {
		if strings.Contains(r.got.SystemPrompt, banned) {
			t.Errorf("reviewer-only context %q leaked into the executor dispatch prompt:\n%s", banned, r.got.SystemPrompt)
		}
	}
}

// TestDispatchExecutorDualPayloadArgvAndStdin proves dual delivery through the
// real agentstep dispatch path: a harness template with {payload} receives the
// same work-item JSON on argv and on stdin (sty_5cf4a1fb / sty_cc35cd0b).
func TestDispatchExecutorDualPayloadArgvAndStdin(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "harness.sh")
	// $1 is {payload}; stdin is the dual channel. Echo both for the test to compare.
	body := "#!/bin/sh\nprintf 'ARGV:%s\\nSTDIN:' \"$1\"\ncat\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	docs := fakeDocs{workflow: dispatchWF, skillBody: "dual-payload rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	g.repoRoot = dir // real cwd for the subprocess (newEngine defaults to /repo)
	harness := script + " {payload}"
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name != "architect" {
			return config.AgentBinding{}, false
		}
		return config.AgentBinding{Command: harness, Tools: "Read,Bash(satelle:*)"}, true
	})
	// Real agentcli.RunnerFromBinding (default) — not a fakeRunner.
	g.newRunner = agentcli.RunnerFromBinding
	res, err := g.DispatchExecutor(context.Background(),
		workitem.Item{ID: "sty_dual", Title: "Dual payload item", Status: "backlog", Body: "body-here"}, "plan")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Dispatched {
		t.Fatal("expected dispatch")
	}
	out := res.Output
	if !strings.HasPrefix(out, "ARGV:") {
		t.Fatalf("argv channel missing: %q", out)
	}
	argvPart, stdinPart, ok := strings.Cut(out, "\nSTDIN:")
	if !ok {
		t.Fatalf("stdin channel missing: %q", out)
	}
	argvPayload := strings.TrimPrefix(argvPart, "ARGV:")
	if argvPayload == "" || argvPayload != stdinPart {
		t.Errorf("dual payload mismatch:\n argv=%q\nstdin=%q", argvPayload, stdinPart)
	}
	if !strings.Contains(argvPayload, "sty_dual") || !strings.Contains(argvPayload, "Dual payload item") {
		t.Errorf("payload missing work item fields: %q", argvPayload)
	}
	// Evidence keeps the placeholder unexpanded.
	if !strings.Contains(res.Command, "{payload}") {
		t.Errorf("Command evidence must keep {payload} literal: %q", res.Command)
	}
	if strings.Contains(res.Command, "sty_dual") {
		t.Errorf("Command evidence must not expand payload body: %q", res.Command)
	}
}

// TestDispatchExecutorAppliesBindingEnv pins AC2: a named binding's (already
// ${VAR}-resolved) Env reaches the dispatched agent's Request, so runProcess can
// layer it onto the child env — how a step points at an alternate backend
// (sty_001558ce).
func TestDispatchExecutorAppliesBindingEnv(t *testing.T) {
	docs := fakeDocs{workflow: dispatchWF, skillBody: "rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	r := &fakeRunner{out: "ok"}
	env := map[string]string{
		"ANTHROPIC_BASE_URL":   "https://api.z.ai/api/anthropic",
		"ANTHROPIC_AUTH_TOKEN": "sk-resolved",
	}
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Bash(satelle:*)", Env: env}, true
	})
	g.newRunner = func(string, string) (agentcli.Runner, error) { return r, nil }
	if _, err := g.DispatchExecutor(context.Background(),
		workitem.Item{ID: "sty_1", Title: "T", Status: "backlog"}, "plan"); err != nil {
		t.Fatal(err)
	}
	if r.got.Env["ANTHROPIC_AUTH_TOKEN"] != "sk-resolved" || r.got.Env["ANTHROPIC_BASE_URL"] != "https://api.z.ai/api/anthropic" {
		t.Errorf("binding env not threaded to the Request: %v", r.got.Env)
	}
}

// TestDispatchExecutorAppliesBindingSettings pins AC2/AC3: a binding's Settings
// table threads through to the built Request as pre-marshalled JSON — the
// {settings} materialisation the retrospective GLM fix depends on.
func TestDispatchExecutorAppliesBindingSettings(t *testing.T) {
	docs := fakeDocs{workflow: dispatchWF, skillBody: "rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	r := &fakeRunner{out: "ok"}
	settings := map[string]any{
		"env": map[string]any{"ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic"},
	}
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		return config.AgentBinding{Command: "fake -p {system} --settings {settings}", Tools: "Read,Bash(satelle:*)", Settings: settings}, true
	})
	g.newRunner = func(string, string) (agentcli.Runner, error) { return r, nil }
	if _, err := g.DispatchExecutor(context.Background(),
		workitem.Item{ID: "sty_1", Title: "T", Status: "backlog"}, "plan"); err != nil {
		t.Fatal(err)
	}
	want := `{"env":{"ANTHROPIC_BASE_URL":"https://api.z.ai/api/anthropic"}}`
	if r.got.Settings != want {
		t.Errorf("binding settings not threaded to the Request: got %q, want %q", r.got.Settings, want)
	}
}

// TestDispatchExecutorMissingBindingRefuses: agent=<name> with no [<name>]
// binding in agents.toml is a broken definition — the dispatch errors (the
// transition is refused), never silently falling back in-loop.
func TestDispatchExecutorMissingBindingRefuses(t *testing.T) {
	g, r := newEngine(t, "", fakeDocs{workflow: dispatchWF, skillBody: "rubric", skillFound: true})
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) { return config.AgentBinding{}, false })
	_, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "plan")
	if err == nil {
		t.Fatal("want refusal for a missing named-agent binding")
	}
	for _, want := range []string{"architect", "agents.toml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should carry %q: %v", want, err)
		}
	}
	if r.got.SystemPrompt != "" {
		t.Error("nothing may run without a binding")
	}
}

// TestDispatchExecutorInLoopStatesUnchanged: agent=executor (and agent-less)
// states dispatch nothing — the in-loop orchestrator performs, today's
// behaviour.
func TestDispatchExecutorInLoopStatesUnchanged(t *testing.T) {
	g, _ := newEngine(t, "", fakeDocs{workflow: dispatchWF, skillBody: "rubric", skillFound: true})
	called := false
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		called = true
		return config.AgentBinding{}, false
	})
	res, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "plan"}, "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if res.Dispatched || called {
		t.Errorf("agent=executor must stay in-loop: %+v (resolver called: %v)", res, called)
	}
}

// TestDispatchExecutorRunFailureSurfaces: a failed named-agent run errors so the
// caller refuses the transition (status unchanged).
func TestDispatchExecutorRunFailureSurfaces(t *testing.T) {
	docs := fakeDocs{workflow: dispatchWF, skillBody: "rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Bash(satelle:*)"}, true
	})
	g.newRunner = func(string, string) (agentcli.Runner, error) { return &fakeRunner{err: errFakeAgent}, nil }
	res, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "plan")
	if err == nil {
		t.Fatal("want the run failure surfaced")
	}
	if !res.Dispatched || !strings.Contains(err.Error(), "architect") {
		t.Errorf("failure should be attributed to the named agent: res=%+v err=%v", res, err)
	}
}

// TestDispatchExecutorRefusesWithoutSatelleCLI: an executor binding whose grant
// omits the read-only satelle CLI is refused at dispatch — an isolated agent cannot
// PULL the story/documents/ledger without it (sty_47d31300), so satelle names the
// fix rather than run a context-starved agent.
func TestDispatchExecutorRefusesWithoutSatelleCLI(t *testing.T) {
	docs := fakeDocs{workflow: dispatchWF, skillBody: "rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	fr := &fakeRunner{out: "ok"}
	g.newRunner = func(string, string) (agentcli.Runner, error) { return fr, nil }

	// No context channel (Claude Read alone is not enough) → refused, agent not run.
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Grep,Glob"}, true
	})
	_, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "plan")
	if err == nil || !strings.Contains(err.Error(), "context channel") {
		t.Fatalf("want refusal naming the context-channel fix, got %v", err)
	}
	if fr.got.SystemPrompt != "" {
		t.Error("must refuse BEFORE running the agent")
	}

	// With Bash(satelle:*) CLI channel → the agent runs.
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Bash(satelle:*)"}, true
	})
	if _, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "plan"); err != nil {
		t.Fatalf("a granted binding should dispatch: %v", err)
	}
	if fr.got.SystemPrompt == "" {
		t.Error("a granted binding must reach the run")
	}
}

// TestDispatchExecutorAcceptsGrokReadFileChannel: a write-capable grok-shaped
// grant with read_file (disk context channel, sty_565a0202) dispatches; a
// write-only / channel-less grant still refuses loudly.
func TestDispatchExecutorAcceptsGrokReadFileChannel(t *testing.T) {
	docs := fakeDocs{workflow: dispatchWF, skillBody: "rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	fr := &fakeRunner{out: "ok"}
	g.newRunner = func(string, string) (agentcli.Runner, error) { return fr, nil }

	// Grok write grant WITH read_file → context channel present → dispatch.
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{
			Command: "fake -p {system}",
			Tools:   "read_file,grep,list_dir,write,search_replace",
		}, true
	})
	if _, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "plan"); err != nil {
		t.Fatalf("read_file context channel should dispatch: %v", err)
	}
	if fr.got.SystemPrompt == "" {
		t.Error("read_file grant must reach the run")
	}

	// Channel-less write grant → still refuses before run.
	fr.got = agentcli.Request{}
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{
			Command: "fake -p {system}",
			Tools:   "write,search_replace,grep,list_dir",
		}, true
	})
	_, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "plan")
	if err == nil || !strings.Contains(err.Error(), "context channel") {
		t.Fatalf("want channel-less refusal, got %v", err)
	}
	if fr.got.SystemPrompt != "" {
		t.Error("channel-less grant must refuse BEFORE running the agent")
	}
}

// TestGrantsSatelleCLIChannels covers the pure grant predicate (CLI + disk).
func TestGrantsSatelleCLIChannels(t *testing.T) {
	cases := []struct {
		tools string
		want  bool
	}{
		{"", false},
		{"Read,Grep,Glob", false},
		{"write,search_replace", false},
		{"Bash(satelle:*)", true},
		{"Read,Bash(satelle:*)", true},
		{"Bash", true},
		{"Bash(*)", true},
		{"*", true},
		{"read_file", true},
		{"read_file,write,search_replace", true},
		{"grep,list_dir,write", false},
	}
	for _, tc := range cases {
		if got := grantsSatelleCLI(tc.tools); got != tc.want {
			t.Errorf("grantsSatelleCLI(%q) = %v, want %v", tc.tools, got, tc.want)
		}
	}
}

// coderWF is a repo opting `in_progress` into a dispatched code-writing agent
// (agent=coder) — the sty_f5bd176f opt-in. The coder is reached from `plan`, a
// PERFORMING node, so the story is engaged while the coder edits.
var coderWF = wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog [shape=Mdiamond]
  plan [agent=planner, prompt="@skill:plan"]
  in_progress [agent=coder, prompt="@skill:code"]
  done [shape=Msquare]
  backlog -> plan -> in_progress -> done
}`)

// TestDispatchExecutorCodeWriterFromNonPerformingProceeds: with the engagement
// lease (sty_8426b9c0) acquired at-start for the TARGET engaging state, edit
// gates no longer depend on the FROM state being performing — so a code-writing
// named agent may dispatch from backlog into plan. The prior FROM-performing
// band-aid (sty_f5bd176f) is removed.
func TestDispatchExecutorCodeWriterFromNonPerformingProceeds(t *testing.T) {
	docs := fakeDocs{workflow: dispatchWF, skillBody: "rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	fr := &fakeRunner{out: "ok"}
	g.newRunner = func(string, string) (agentcli.Runner, error) { return fr, nil }
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Edit,Write,Bash(satelle:*)"}, true
	})
	res, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "plan")
	if err != nil {
		t.Fatalf("code-writer from non-performing FROM should proceed under lease model: %v", err)
	}
	if !res.Dispatched {
		t.Fatalf("expected dispatched, got %+v", res)
	}
	if fr.got.SystemPrompt == "" {
		t.Error("the agent must reach the run")
	}
}

// TestDispatchExecutorCodeWriterFromPerformingProceeds: the same code-writing grant
// dispatched from a PERFORMING FROM state (plan → in_progress in coderWF, where
// `plan` performs) is allowed — the story is legitimately engaged while the coder
// edits, so the lock does not fire.
func TestDispatchExecutorCodeWriterFromPerformingProceeds(t *testing.T) {
	docs := fakeDocs{workflow: coderWF, skillBody: "code rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	fr := &fakeRunner{out: "built the slice"}
	g.newRunner = func(string, string) (agentcli.Runner, error) { return fr, nil }
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name != "coder" {
			return config.AgentBinding{}, false
		}
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Edit,Write,Bash(satelle:*)"}, true
	})
	res, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "plan"}, "in_progress")
	if err != nil {
		t.Fatalf("a code-writer from a performing state should dispatch: %v", err)
	}
	if !res.Dispatched || res.Agent != "coder" {
		t.Fatalf("result = %+v, want dispatched by coder", res)
	}
	if fr.got.SystemPrompt == "" {
		t.Error("the coder must reach the run")
	}
}

// onEnterParkWF: reviewer park with on_enter_agent triage — node name is
// "parked" (not "blocked") so the mechanism has no state-name dependence.
var onEnterParkWF = wfDoc("on-enter-park", `"*"`, `digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  parked [agent=reviewer, prompt="@skill:park-gate", on_enter_agent=triage, on_enter_prompt="@skill:triage-skill"]
  done [shape=Msquare]
  backlog -> in_progress -> done
  in_progress -> parked [agent=reviewer, prompt="@skill:park-gate"]
  parked -> in_progress
}`)

// TestDispatchOnEnterAgentFromPerforming: AC1 (sty_5cabe26f) — a park node with
// agent=reviewer still dispatches on_enter_agent once on entry when FROM is
// performing. Tools/model come from the named binding.
func TestDispatchOnEnterAgentFromPerforming(t *testing.T) {
	docs := fakeDocs{workflow: onEnterParkWF, skillBody: "triage rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	fr := &fakeRunner{out: "triaged"}
	g.newRunner = func(string, string) (agentcli.Runner, error) { return fr, nil }
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name != "triage" {
			return config.AgentBinding{}, false
		}
		return config.AgentBinding{
			Role: "agent", Command: "fake -p {system}", Model: "opus",
			Tools: "Read,Write,Edit,Bash(satelle:*)",
		}, true
	})
	res, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "parked")
	if err != nil {
		t.Fatalf("on_enter from performing state should dispatch: %v", err)
	}
	if !res.Dispatched || res.Agent != "triage" || res.Skill != "triage-skill" {
		t.Fatalf("result = %+v, want dispatched by triage with triage-skill", res)
	}
	if res.Model != "opus" {
		t.Errorf("model = %q, want opus from binding", res.Model)
	}
	if fr.got.SystemPrompt == "" {
		t.Error("triage agent must reach the run")
	}
	if !strings.Contains(fr.got.SystemPrompt, "triage rubric") {
		t.Errorf("system prompt should carry on_enter skill rubric, got %q", fr.got.SystemPrompt)
	}
}

// TestDispatchOnEnterAgentCodeWriterFromNonPerformingProceeds: lease model
// (sty_8426b9c0) removes the FROM-performing code-writer lock for on_enter_agent
// as well — Write/Edit grant from backlog into park is allowed (the lease, not
// FROM status, authorises engagement for the edit gate).
func TestDispatchOnEnterAgentCodeWriterFromNonPerformingProceeds(t *testing.T) {
	wf := wfDoc("on-enter-from-backlog", `"*"`, `digraph w {
  backlog [shape=Mdiamond]
  parked [agent=reviewer, prompt="@skill:park-gate", on_enter_agent=triage, on_enter_prompt="@skill:triage-skill"]
  done [shape=Msquare]
  backlog -> parked
  parked -> done
}`)
	docs := fakeDocs{workflow: wf, skillBody: "triage rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	fr := &fakeRunner{out: "triaged"}
	g.newRunner = func(string, string) (agentcli.Runner, error) { return fr, nil }
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{Role: "agent", Command: "fake -p {system}", Tools: "Read,Edit,Write,Bash(satelle:*)"}, true
	})
	res, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "parked")
	if err != nil {
		t.Fatalf("on_enter code-writer from non-performing FROM should proceed: %v", err)
	}
	if !res.Dispatched || res.Agent != "triage" {
		t.Fatalf("result = %+v, want dispatched by triage", res)
	}
}

// TestDispatchPayloadCarriesIdNotDocuments: AC2 (sty_47d31300) — the dispatch
// payload is the fetch HANDLE (the item record with its id), not a PUSH of
// documents or the ledger. The agent pulls those by id; they are never marshalled
// into stdin.
func TestDispatchPayloadCarriesIdNotDocuments(t *testing.T) {
	docs := fakeDocs{workflow: dispatchWF, skillBody: "rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	fr := &fakeRunner{out: "ok"}
	g.newRunner = func(string, string) (agentcli.Runner, error) { return fr, nil }
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Bash(satelle:*)"}, true
	})
	if _, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_pull1", Status: "backlog", Title: "T"}, "plan"); err != nil {
		t.Fatal(err)
	}
	p := fr.got.Payload
	if !strings.Contains(p, `"sty_pull1"`) {
		t.Errorf("payload must carry the story id as the fetch handle: %s", p)
	}
	for _, forbidden := range []string{`"documents"`, `"ledger"`, `"attachments"`, `"plan_body"`} {
		if strings.Contains(p, forbidden) {
			t.Errorf("payload must NOT push %s (pull-context, not push): %s", forbidden, p)
		}
	}
}

// streamingFakeRunner emits normalized events DURING Run, standing in for a real
// subprocess that streams incremental output.
type streamingFakeRunner struct {
	lines []string
	got   agentcli.Request
}

func (s *streamingFakeRunner) Name() string    { return "streaming-fake" }
func (s *streamingFakeRunner) Command() string { return "streaming-fake" }
func (s *streamingFakeRunner) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	s.got = req
	for _, l := range s.lines {
		if req.OnEvent != nil {
			req.OnEvent(agentcli.Event{Kind: agentcli.EventMessage, Text: l})
		}
	}
	return []byte("ok"), nil
}

// TestDispatchExecutorWiresLiveSink pins normalized event logging.
func TestDispatchExecutorWiresLiveSink(t *testing.T) {
	dir := t.TempDir()
	docs := fakeDocs{workflow: dispatchWF, skillBody: "rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	g.SetLogDir(dir, logfile.Config{})
	r := &streamingFakeRunner{lines: []string{"first line\n", "second line\n"}}
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Bash(satelle:*)"}, true
	})
	g.newRunner = func(string, string) (agentcli.Runner, error) { return r, nil }

	if _, err := g.DispatchExecutor(context.Background(),
		workitem.Item{ID: "sty_1", Status: "backlog"}, "plan"); err != nil {
		t.Fatal(err)
	}
	if r.got.OnEvent == nil {
		t.Fatal("DispatchExecutor did not pass an OnEvent handler through to the runner's Request")
	}
	if r.got.Sink != nil {
		t.Fatal("raw Sink should be disabled unless SATELLE_AGENT_TRACE_RAW is opted in")
	}
	matches, err := filepath.Glob(filepath.Join(dir, "dispatch", "dispatch-*.log"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected exactly one dispatch log file, got %v (err %v)", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\tmessage\tfirst line") || !strings.Contains(string(data), "\tmessage\tsecond line") {
		t.Errorf("dispatch log missing streamed lines: %q", data)
	}
}

func TestDispatchExecutorRawTraceRequiresOptIn(t *testing.T) {
	t.Setenv("SATELLE_AGENT_TRACE_RAW", "1")
	dir := t.TempDir()
	docs := fakeDocs{workflow: dispatchWF, skillBody: "rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	g.SetLogDir(dir, logfile.Config{})
	r := &streamingFakeRunner{lines: []string{"normalized"}}
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Bash(satelle:*)"}, true
	})
	g.newRunner = func(string, string) (agentcli.Runner, error) { return r, nil }

	if _, err := g.DispatchExecutor(context.Background(),
		workitem.Item{ID: "sty_raw", Status: "backlog"}, "plan"); err != nil {
		t.Fatal(err)
	}
	if r.got.Sink == nil {
		t.Fatal("raw Sink should be present after explicit opt-in")
	}
	matches, err := filepath.Glob(filepath.Join(dir, "dispatch", "dispatch-*-raw.log"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected one opt-in raw log, got %v (err %v)", matches, err)
	}
}

// TestDispatchExecutorNoSinkWithoutLogDir pins AC2's other backward-compat half:
// when no log dir is configured (as in most engine tests, and any embedder that
// never called SetLogDir), the Request carries a nil Sink — the pre-existing
// callers are unaffected by this change.
func TestDispatchExecutorNoSinkWithoutLogDir(t *testing.T) {
	docs := fakeDocs{workflow: dispatchWF, skillBody: "rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	r := &fakeRunner{out: "ok"}
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Bash(satelle:*)"}, true
	})
	g.newRunner = func(string, string) (agentcli.Runner, error) { return r, nil }
	if _, err := g.DispatchExecutor(context.Background(),
		workitem.Item{ID: "sty_1", Status: "backlog"}, "plan"); err != nil {
		t.Fatal(err)
	}
	if r.got.Sink != nil {
		t.Error("no log dir is configured — the Request should carry a nil Sink")
	}
}

func TestGateRefusesUndeclaredEdge(t *testing.T) {
	// in_progress→integrated is NOT a declared edge in testWorkflow. The gate must
	// refuse it (error) so a story cannot skip a gate by jumping across an
	// undeclared edge — and the reviewer must not run.
	g, r := newEngine(t, `{"decision":"accept"}`,
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
	g, _ := newEngine(t, "", fakeDocs{})

	good := verb.CreateDraft{Kind: "story", Title: "Add X", Body: "Make the thing do X", AcceptanceCriteria: "1. a", Category: "feature"}
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
	g, r := newEngine(t, "", fakeDocs{skillFound: false})
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
var validDraft = verb.CreateDraft{Kind: "story", Title: "Add X", Body: "Make the thing do X", AcceptanceCriteria: "1. it does X", Category: "feature"}

// createWF is a wildcard workflow that DECLARES a content/alignment create
// reviewer via its create_review frontmatter (sty_b031b29f) — the binding lives on
// the workflow, not a hardcoded constant.
const createWF = "---\nname: " + baselineWorkflow + "\ntype: workflow\napplies_to: [\"*\"]\ncreate_review: my-create-review\n---\n# wf\n"

// plainWF is a wildcard workflow with NO create_review declaration.
const plainWF = "---\nname: " + baselineWorkflow + "\ntype: workflow\napplies_to: [\"*\"]\n---\n# wf\n"

// When the active workflow declares create_review, a structurally-valid draft is
// judged by that reviewer; its reject blocks creation with the notes.
func TestReviewCreateContentReject(t *testing.T) {
	g, _ := newEngine(t, `{"decision":"reject","notes":"ACs do not match the goal"}`,
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
	g, _ := newEngine(t, `{"decision":"accept","notes":"aligned"}`,
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
	g, r := newEngine(t, `{"decision":"reject","notes":"should not run"}`,
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
	g, r := newEngine(t, `{"decision":"accept"}`,
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
	g, r := newEngine(t, "  Moved from in_progress to done after the criteria were met.\n",
		fakeDocs{workflow: stepWF, skillBody: "summariser rubric", skillFound: true})
	s, err := g.Summarise(context.Background(), workitem.Item{Status: "in_progress"}, "in_progress", "done")
	if err != nil {
		t.Fatal(err)
	}
	if s.Text != "Moved from in_progress to done after the criteria were met." {
		t.Errorf("summary = %q", s.Text)
	}
	// The skill doc's body (frontmatter included, as authored substrate carries
	// it) rides as the system prompt — the rubric must be in it.
	if !strings.Contains(r.got.SystemPrompt, "summariser rubric") {
		t.Errorf("summariser rubric should ride in the system prompt, got %q", r.got.SystemPrompt)
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

// TestSummariseUsesNamedBinding (sty_8ee40f94): agent= on the step node selects
// the harness/model; default [reviewer] is unchanged when agent=reviewer.
func TestSummariseUsesNamedBinding(t *testing.T) {
	const namedWF = "---\nname: " + baselineWorkflow + "\ntype: workflow\n---\n" + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  step        [agent=summariser-x, prompt="@skill:satelle-step-summary", mandatory=true]
  done        [shape=Msquare]
  backlog -> in_progress -> done
}
` + "```"
	g, defaultR := newEngine(t, "default runner must not run",
		fakeDocs{workflow: namedWF, skillBody: "summariser rubric", skillFound: true})
	namedR := &fakeRunner{out: "  Named summariser recap.\n"}
	var gotCmd string
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name != "summariser-x" {
			return config.AgentBinding{}, false
		}
		return config.AgentBinding{
			Command: "cheap-sum -p {system}", Role: "reviewer",
			Tools: "read_file,grep", Model: "cheap-model", Effort: "low",
		}, true
	})
	g.newRunner = func(_iface, cmd string) (agentcli.Runner, error) {
		gotCmd = cmd
		return namedR, nil
	}
	s, err := g.Summarise(context.Background(), workitem.Item{Status: "in_progress"}, "in_progress", "done")
	if err != nil {
		t.Fatal(err)
	}
	if s.Text != "Named summariser recap." {
		t.Errorf("summary = %q", s.Text)
	}
	if gotCmd != "cheap-sum -p {system}" {
		t.Errorf("newRunner cmd = %q, want cheap-sum template", gotCmd)
	}
	if namedR.got.AllowedTools != "read_file,grep" || namedR.got.Model != "cheap-model" {
		t.Errorf("binding grant not applied: tools=%q model=%q", namedR.got.AllowedTools, namedR.got.Model)
	}
	if s.Model != "cheap-model" {
		t.Errorf("SummaryResult.Model = %q, want cheap-model", s.Model)
	}
	if defaultR.got.SystemPrompt != "" {
		t.Error("default [reviewer] runner must not run for a named step binding")
	}

	// Default agent=reviewer still uses g.runner / g.model.
	g2, r2 := newEngine(t, "legacy default path",
		fakeDocs{workflow: stepWF, skillBody: "summariser rubric", skillFound: true})
	s2, err := g2.Summarise(context.Background(), workitem.Item{Status: "in_progress"}, "in_progress", "done")
	if err != nil {
		t.Fatal(err)
	}
	if s2.Text != "legacy default path" {
		t.Errorf("default summary = %q", s2.Text)
	}
	if r2.got.SystemPrompt == "" {
		t.Error("default path must use bootstrap runner")
	}

	// Missing named binding is soft-fail for mandatory.
	g3, _ := newEngine(t, "", fakeDocs{workflow: namedWF, skillBody: "rubric", skillFound: true})
	g3.SetNamedAgents(func(string) (config.AgentBinding, bool) { return config.AgentBinding{}, false })
	if _, err := g3.Summarise(context.Background(), workitem.Item{Status: "in_progress"}, "in_progress", "done"); err == nil {
		t.Error("mandatory summary with missing binding must error")
	}
}

// When the active workflow declares NO step node, the summariser does not run —
// transparent opt-in (sty_9a139c78).
func TestSummariseSkippedWhenNotDeclared(t *testing.T) {
	g, r := newEngine(t, "should not run", fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	s, err := g.Summarise(context.Background(), workitem.Item{Status: "in_progress"}, "in_progress", "done")
	if err != nil {
		t.Fatal(err)
	}
	if s.Text != "" {
		t.Errorf("no step node declared → no summary, got %q", s.Text)
	}
	if r.got.SystemPrompt != "" {
		t.Errorf("summariser must not run when the workflow declares no step node")
	}
}

// A mandatory step node whose rubric is absent surfaces an error (the gap is not
// silently swallowed); a non-mandatory one stays best-effort (empty, no error).
func TestSummariseMandatoryVsOptionalWhenAbsent(t *testing.T) {
	g, _ := newEngine(t, "", fakeDocs{workflow: stepWF, skillFound: false}) // mandatory
	if _, err := g.Summarise(context.Background(), workitem.Item{Status: "in_progress"}, "in_progress", "done"); err == nil {
		t.Error("mandatory step summary with an absent rubric should error")
	}
	g2, _ := newEngine(t, "", fakeDocs{workflow: stepWFOptional, skillFound: false}) // optional
	if s, err := g2.Summarise(context.Background(), workitem.Item{Status: "in_progress"}, "in_progress", "done"); err != nil || s.Text != "" {
		t.Errorf("optional step summary with an absent rubric should be empty/no-error, got %q/%v", s.Text, err)
	}
}

func TestSummariseEmptyWhenRubricAbsent(t *testing.T) {
	g, r := newEngine(t, "should not run", fakeDocs{workflow: stepWFOptional, skillFound: false})
	s, err := g.Summarise(context.Background(), workitem.Item{Status: "in_progress"}, "in_progress", "done")
	if err != nil {
		t.Fatal(err)
	}
	if s.Text != "" {
		t.Errorf("want empty summary when rubric absent, got %q", s.Text)
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
var webWorkflow = wfDoc("satelle-web-workflow", `"web"`, `digraph w {
  backlog -> in_progress
  in_progress -> done [reviewer_skill="satelle-web-done-review"]
}`)

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
			g, _ := newEngine(t, `{"decision":"accept","notes":""}`, docs)
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

const checkSkill = "---\nname: satelle-story-integration-review\ntype: skill\ndescription: integration functional check\ncheck: \"run-the-suite\"\n---\n# Integration gate\nRuns the suite.\n"

func TestFunctionalCheckGate(t *testing.T) {
	// A workflow edge whose reviewer skill carries a `check:` runs deterministically.
	wf := wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog -> in_progress
  in_progress -> integrated [reviewer_skill="satelle-story-integration-review"]
  integrated -> done
}`)

	t.Run("pass accepts, agent not run", func(t *testing.T) {
		g, r := newEngine(t, `{"decision":"reject"}`, fakeDocs{workflow: wf, skillBody: checkSkill, skillFound: true})
		var ran string
		g.check = func(_ context.Context, dir, command, payload string) (string, error) {
			ran = command
			if !strings.Contains(payload, `"to":"integrated"`) {
				t.Errorf("check payload missing the transition: %q", payload)
			}
			return "ok\n", nil
		}
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
		g, _ := newEngine(t, ``, fakeDocs{workflow: wf, skillBody: checkSkill, skillFound: true})
		g.check = func(_ context.Context, dir, command, payload string) (string, error) {
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
// (sty_ef08ce2a, extended by sty_3c1a2a9d): an execution resolves by its KIND
// ("execution") and a task HEADER by its kind ("task") to a workflow declaring
// applies_to:["execution","task"], and NEITHER falls through to the wildcard
// story workflow — a header's authored category ("substrate", "docs", …) is
// never the resolution key. A story still resolves by category to the wildcard.
func TestExecutionResolvesToTaskExecutionWorkflow(t *testing.T) {
	storyWild := docindex.Doc{Name: "satelle-project-workflow", Embedded: false,
		Body: "---\nscope: project\napplies_to: [\"*\"]\n---\n"}
	taskExec := docindex.Doc{Name: "satelle-task-workflow", Embedded: false,
		Body: "---\nscope: project\napplies_to: [\"execution\", \"task\"]\n---\n"}
	all := []docindex.Doc{storyWild, taskExec}

	// The resolution key for an execution is its kind, not its (empty) category.
	if got := workflowCategory(workitem.Item{Kind: workitem.KindExecution}); got != "execution" {
		t.Fatalf("workflowCategory(execution) = %q, want \"execution\"", got)
	}
	// The resolution key for a task header is its kind, not its authored category.
	if got := workflowCategory(workitem.Item{Kind: workitem.KindTask, Category: "substrate"}); got != "task" {
		t.Fatalf("workflowCategory(task) = %q, want \"task\"", got)
	}
	// An execution resolves to the task-execution workflow, not the story wildcard.
	got := OrderedWorkflows(all, workflowCategory(workitem.Item{Kind: workitem.KindExecution}))
	if len(got) == 0 || got[0].Name != "satelle-task-workflow" {
		t.Fatalf("execution head = %v, want satelle-task-workflow (not the story workflow)", names(got))
	}
	// An unstamped task header ALSO resolves to the task workflow (sty_3c1a2a9d):
	// its authored category matches no workflow, and falling through to the
	// wildcard story workflow was the misrouting that ran headers through story gates.
	tk := OrderedWorkflows(all, workflowCategory(workitem.Item{Kind: workitem.KindTask, Category: "substrate"}))
	if len(tk) == 0 || tk[0].Name != "satelle-task-workflow" {
		t.Fatalf("task-header head = %v, want satelle-task-workflow (not the story workflow)", names(tk))
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
	skills, model, _, declared := reviewerSkillsFor(dotWF, "in_progress", "committed")
	if !declared || len(skills) != 1 || skills[0] != "satelle-commit-push-reviewer" || model != "" {
		t.Fatalf("in_progress->committed: skills=%v model=%q declared=%v", skills, model, declared)
	}
	if _, _, _, declared := reviewerSkillsFor(dotWF, "in_progress", "nope"); declared {
		t.Errorf("an undeclared edge should report declared=false")
	}
	if skills, _, _, declared := reviewerSkillsFor(dotWF, "committed", "done"); !declared || len(skills) != 0 {
		t.Errorf("committed->done should be declared and ungated: skills=%v declared=%v", skills, declared)
	}
}

// A container close gate is judged from the children SATELLE injects into the
// payload (resolved from the DB), not any on-disk story mirror (sty_fa1e02e1).
func TestGatePayloadIncludesChildren(t *testing.T) {
	g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
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

// TestGatePayloadIncludesDocs (sty_58fa970e): attachments ride in the payload
// so a Bash-less reviewer can judge without any disk path.
func TestGatePayloadIncludesDocs(t *testing.T) {
	g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	g.SetDocsResolver(func(_ context.Context, itemID string) []DocState {
		if itemID != "sty_docs" {
			t.Errorf("docs resolver itemID = %q", itemID)
		}
		return []DocState{
			{Name: "plan", Type: "plan", Body: "# plan body for AC1"},
			{Name: "step-summary-x", Type: "step-summary", Body: "did the thing"},
		}
	})
	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_docs", Status: "in_progress"}, "done"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"docs"`, `"name":"plan"`, "plan body for AC1", "step-summary"} {
		if !strings.Contains(r.got.Payload, want) {
			t.Errorf("payload missing %q:\n%s", want, r.got.Payload)
		}
	}
}

// TestChangeRecordExcludedFromGatePayload (sty_948ad5df AC4): type:change
// patches are disk retention only and must not ride the gate docs payload.
func TestChangeRecordExcludedFromGatePayload(t *testing.T) {
	g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	g.SetDocsResolver(func(_ context.Context, itemID string) []DocState {
		return []DocState{
			{Name: "plan", Type: "plan", Body: "# plan body"},
			{Name: "change-in_progress-integration", Type: "change", Body: "diff --git a/secret.go\n+planted-secret-in-change-attachment"},
			{Name: "step-summary-x", Type: "step-summary", Body: "did the thing"},
		}
	})
	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_change", Status: "in_progress"}, "done"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.got.Payload, "plan body") {
		t.Error("plan doc must still ride the payload")
	}
	if strings.Contains(r.got.Payload, "planted-secret-in-change-attachment") {
		t.Error("type:change body must not appear in gate payload")
	}
	if strings.Contains(r.got.Payload, `"name":"change-in_progress-integration"`) {
		t.Error("type:change doc must not be listed in gate payload docs")
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
	g, r := newEngine(t, "  recap.\n", fakeDocs{workflow: stepWF, skillBody: "rubric", skillFound: true})
	g.SetReviewerModel("sonnet")
	if _, err := g.Summarise(context.Background(), workitem.Item{Status: "in_progress"}, "in_progress", "done"); err != nil {
		t.Fatal(err)
	}
	if r.got.Model != "sonnet" {
		t.Errorf("runner Request model = %q, want sonnet", r.got.Model)
	}
}

// TestGateNamedReviewerBinding (sty_a476a2f8 / sty_68dafd5f): edge agent=<name>
// resolves that binding's model AND builds its harness via newRunner (not
// g.runner); agent=reviewer / omitted uses [reviewer] + bootstrap runner.
func TestGateNamedReviewerBinding(t *testing.T) {
	wf := wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog [shape=Mdiamond]
  done [shape=Msquare]
  backlog -> done [agent=reviewer-deep, prompt="@skill:rev-a"]
}`)
	g, r := newEngine(t, `{"decision":"accept","notes":"ok"}`, fakeDocs{
		workflow: wf, skillBody: "rubric", skillFound: true,
	})
	g.SetReviewerModel("grok-4.5")
	const deepCmd = "deep-reviewer-cli -p --append-system-prompt {system}"
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name == "reviewer-deep" {
			return config.AgentBinding{
				Command: deepCmd, Tools: "Read,Grep", Model: "opus", Role: "reviewer",
			}, true
		}
		return config.AgentBinding{}, false
	})
	var sawCmd string
	newRunnerCalls := 0
	g.newRunner = func(iface, cmd string) (agentcli.Runner, error) {
		newRunnerCalls++
		sawCmd = cmd
		return r, nil // hermetic: same fake runner, built for the named binding
	}
	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_x", Status: "backlog"}, "done")
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Gated || !dec.Accept {
		t.Fatalf("gate = %+v", dec)
	}
	if r.got.Model != "opus" {
		t.Errorf("runner Request.Model = %q, want opus (named reviewer-deep)", r.got.Model)
	}
	if sawCmd != deepCmd {
		t.Errorf("newRunner command = %q, want named binding command %q", sawCmd, deepCmd)
	}
	if newRunnerCalls != 1 {
		t.Errorf("newRunner calls = %d, want 1 for named gate", newRunnerCalls)
	}

	// Default [reviewer] when agent omitted/reviewer — uses g.runner, not newRunner.
	wf2 := wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog [shape=Mdiamond]
  done [shape=Msquare]
  backlog -> done [agent=reviewer, prompt="@skill:rev-a"]
}`)
	g2, r2 := newEngine(t, `{"decision":"accept","notes":"ok"}`, fakeDocs{
		workflow: wf2, skillBody: "rubric", skillFound: true,
	})
	g2.SetReviewerModel("grok-4.5")
	g2.newRunner = func(iface, cmd string) (agentcli.Runner, error) {
		t.Errorf("default [reviewer] must not call newRunner (got iface=%q cmd=%q)", iface, cmd)
		return r2, nil
	}
	if _, err := g2.Gate(context.Background(), workitem.Item{ID: "sty_x", Status: "backlog"}, "done"); err != nil {
		t.Fatal(err)
	}
	if r2.got.Model != "grok-4.5" {
		t.Errorf("default reviewer model = %q, want grok-4.5", r2.got.Model)
	}
}

// TestReviewerSkillsForDOTAgent: edge agent= is returned alongside skills.
func TestReviewerSkillsForDOTAgent(t *testing.T) {
	const body = "---\nname: w\n---\n```dot\n" + `digraph w {
  a -> b [agent=reviewer-deep, prompt="@skill:rev"]
}
` + "```\n"
	skills, agent, _, declared := reviewerSkillsFor(body, "a", "b")
	if !declared || len(skills) != 1 || skills[0] != "rev" || agent != "reviewer-deep" {
		t.Fatalf("skills=%v agent=%q declared=%v", skills, agent, declared)
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
	g, _ := newEngine(t, "", fakeDocs{workflow: plainWF, extraWorkflows: []docindex.Doc{wfFeature, wfChore}})
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
	// (3) An unresolved create_review binding is flagged (sty_51ad783b): it would
	// silently degrade creation to deterministic-only; resolved -> clean.
	wfCreate := docindex.Doc{Name: "y", Embedded: false,
		Body: "---\nname: y\ntype: workflow\napplies_to: [\"fix\"]\ncreate_review: my-create-review\n---\n# w\n"}
	crMiss := WorkflowConsistency([]docindex.Doc{wfCreate}, func(s string) bool { return s != "my-create-review" })
	if len(crMiss) == 0 || !strings.Contains(strings.Join(crMiss, "\n"), "create_review") {
		t.Errorf("unresolved create_review should be flagged, got %v", crMiss)
	}
	if ok := WorkflowConsistency([]docindex.Doc{wfCreate}, func(string) bool { return true }); len(ok) != 0 {
		t.Errorf("a resolved create_review is clean, got %v", ok)
	}
}

// TestCodedEstimateGate runs the EMBEDDED estimate/actual skill's self-contained
// check end-to-end — real bash, the transition payload on stdin (sty_f804caaa):
// begin-work without an estimate tag rejects, close without an actual tag
// rejects, other edges pass — and the agent is never invoked.
func TestCodedEstimateGate(t *testing.T) {
	var body string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == "satelle-estimate-actual-review" {
			body = d.Body
		}
	}
	if body == "" || !strings.Contains(body, "```check") {
		t.Fatalf("embedded estimate skill must carry a self-contained check block")
	}
	g, r := newEngine(t, `{"decision":"reject"}`, fakeDocs{skillBody: body, skillFound: true})
	g.repoRoot = t.TempDir() // the check runs in the repo root — use a real dir

	cases := []struct {
		name   string
		to     string
		tags   []string
		accept bool
		want   string // substring of the reject notes
	}{
		{"in_progress without estimate rejects", "in_progress", []string{"cli"}, false, "no plan estimate recorded"},
		{"in_progress with estimate accepts", "in_progress", []string{"estimate-minutes:30"}, true, ""},
		{"done without actual rejects", "done", []string{"estimate-tokens:5000"}, false, "no actual recorded"},
		{"done with actual accepts", "done", []string{"actual-tokens:9000"}, true, ""},
		{"other edges pass", "integration", nil, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dec, err := g.runReviewer(context.Background(),
				workitem.Item{ID: "sty_x", Kind: workitem.KindStory, Status: "backlog", Tags: c.tags},
				c.to, "satelle-estimate-actual-review", "")
			if err != nil {
				t.Fatal(err)
			}
			if !dec.Gated || dec.Accept != c.accept {
				t.Fatalf("decision = %+v, want gated accept=%v", dec, c.accept)
			}
			if c.want != "" && !strings.Contains(dec.Notes, c.want) {
				t.Errorf("notes = %q, want substring %q", dec.Notes, c.want)
			}
		})
	}
	if r.got.SystemPrompt != "" {
		t.Error("the coded gate must never invoke the agent")
	}
}

// TestRetrospectDispatchesNamedAgent: Retrospect resolves the [retrospective]
// binding, runs its harness with the retrospective rubric + the story payload,
// and returns the dispatch result carrying the resolved model (sty_b53730e2).
func TestRetrospectDispatchesNamedAgent(t *testing.T) {
	g, _ := newEngine(t, "PROPOSALS FILED: none", fakeDocs{skillBody: "retro rubric", skillFound: true})
	r := &fakeRunner{out: "PROPOSALS FILED: none"}
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name != "retrospective" {
			return config.AgentBinding{}, false
		}
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Bash(satelle:*)", Model: "glm-4.6"}, true
	})
	g.newRunner = func(string, string) (agentcli.Runner, error) { return r, nil }
	res, err := g.Retrospect(context.Background(), workitem.Item{ID: "sty_1", Title: "T", Status: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Dispatched || res.Agent != "retrospective" || res.Model != "glm-4.6" {
		t.Fatalf("res = %+v", res)
	}
	if !strings.Contains(r.got.SystemPrompt, "retro rubric") {
		t.Errorf("retrospective rubric missing from prompt:\n%s", r.got.SystemPrompt)
	}
	if !strings.Contains(r.got.Payload, "sty_1") {
		t.Errorf("story payload missing: %q", r.got.Payload)
	}
}

// TestRetrospectMissingBindingErrors: no [retrospective] binding is a clear
// refusal (the mechanism never silently no-ops).
func TestRetrospectMissingBindingErrors(t *testing.T) {
	g, _ := newEngine(t, "", fakeDocs{})
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) { return config.AgentBinding{}, false })
	if _, err := g.Retrospect(context.Background(), workitem.Item{ID: "sty_1"}); err == nil {
		t.Fatal("want an error naming the missing [retrospective] binding")
	}
}

// TestRetrospectRequiresSatelleCLI: the binding must grant the satelle CLI so the
// agent can pull the story and file proposals — refuse otherwise.
func TestRetrospectRequiresSatelleCLI(t *testing.T) {
	g, _ := newEngine(t, "", fakeDocs{skillBody: "x", skillFound: true})
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Grep,Glob"}, true
	})
	g.newRunner = func(string, string) (agentcli.Runner, error) { return &fakeRunner{}, nil }
	if _, err := g.Retrospect(context.Background(), workitem.Item{ID: "sty_1"}); err == nil {
		t.Fatal("want an error when the grant has no context channel")
	}
}

// --- AC2 dispatch-outcome telemetry (sty_b73c3236) --------------------------
//
// The dispatch engine records STRUCTURED, queryable outcomes for every agent
// failure mode a wrapping verb never sees — a killed/timed-out/no-verdict
// subprocess: classifyOutcome names WHY, and each reviewer/summariser/executor
// retry/failure/timeout site emits a telemetry event via the wired sink. These
// pin that wiring (the gap satelle-code-ac-review flagged).

type telemetryRec struct {
	storyID, actor, kind string
	data                 map[string]any
}

// captureTelemetry wires g's telemetry sink to append to the returned slice, so
// a test can assert exactly which events the engine emitted, with what outcome.
func captureTelemetry(g *Engine) *[]telemetryRec {
	recs := &[]telemetryRec{}
	g.SetTelemetry(func(_ context.Context, storyID, actor, kind string, data map[string]any) {
		*recs = append(*recs, telemetryRec{storyID, actor, kind, data})
	})
	return recs
}

func countKind(recs []telemetryRec, kind string) int {
	n := 0
	for _, r := range recs {
		if r.kind == kind {
			n++
		}
	}
	return n
}

func hasOutcome(recs []telemetryRec, kind, want string) bool {
	for _, r := range recs {
		if r.kind == kind {
			if o, _ := r.data["outcome"].(string); o == want {
				return true
			}
		}
	}
	return false
}

// classifyOutcome names WHY a dispatched invocation failed — the label that
// makes the telemetry queryable rather than a free-text body string.
func TestClassifyOutcome(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil is a parsed no-verdict, not a run error", nil, "no-verdict"},
		{"a bounded deadline is a timeout", context.DeadlineExceeded, "timeout"},
		{"a killed subprocess surfaces signal:killed", errors.New("agentcli: claude: signal: killed"), "signal:killed"},
		{"anything else is a generic error", errors.New("boom"), "error"},
	}
	for _, c := range cases {
		if got := classifyOutcome(c.err); got != c.want {
			t.Errorf("%s: classifyOutcome(%v) = %q, want %q", c.name, c.err, got, c.want)
		}
	}
}

// A reviewer that never returns a verdict records one agent-retry per transient
// attempt (carrying the classified outcome) and one terminal agent-failure — the
// structured capture AC2 asks for, not only a reviewer.log line.
func TestGate_recordsRetryAndFailureTelemetry(t *testing.T) {
	docs := fakeDocs{workflow: testWorkflow, skillBody: "rubric body", skillFound: true}
	r := &scriptedRunner{results: []struct {
		out string
		err error
	}{
		{out: "no verdict here"}, // transient no-verdict → agent-retry outcome=no-verdict
		{err: errFakeAgent},      // transient run error   → agent-retry outcome=error
	}}
	g := New(r, docs, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }
	recs := captureTelemetry(g)

	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "done"); err == nil {
		t.Fatal("gate should fail once every attempt is a transient non-verdict")
	}
	if got := countKind(*recs, "agent-retry"); got != defaultReviewerAttempts {
		t.Errorf("want %d agent-retry events (one per attempt), got %d", defaultReviewerAttempts, got)
	}
	if got := countKind(*recs, "agent-failure"); got != 1 {
		t.Errorf("want exactly 1 terminal agent-failure event, got %d", got)
	}
	if !hasOutcome(*recs, "agent-retry", "no-verdict") {
		t.Error("a no-verdict attempt should record outcome=no-verdict")
	}
	if !hasOutcome(*recs, "agent-retry", "error") {
		t.Error("an errored attempt should record outcome=error")
	}
	for _, rec := range *recs {
		if rec.actor != "reviewer" {
			t.Errorf("reviewer telemetry actor = %q, want reviewer", rec.actor)
		}
		if s, _ := rec.data["skill"].(string); s == "" {
			t.Errorf("telemetry event %q missing the skill field", rec.kind)
		}
	}
}

// A reviewer deadline is a bound, not contention — it records a single
// agent-timeout and fails fast (no retry, no agent-failure).
func TestGate_recordsTimeoutTelemetry(t *testing.T) {
	docs := fakeDocs{workflow: testWorkflow, skillBody: "rubric body", skillFound: true}
	r := &scriptedRunner{results: []struct {
		out string
		err error
	}{{err: context.DeadlineExceeded}}}
	g := New(r, docs, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }
	recs := captureTelemetry(g)

	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "done"); err == nil {
		t.Fatal("a reviewer deadline should surface an error")
	}
	if got := countKind(*recs, "agent-timeout"); got != 1 {
		t.Errorf("want exactly 1 agent-timeout event, got %d", got)
	}
	if got := countKind(*recs, "agent-failure"); got != 0 {
		t.Errorf("a fail-fast timeout must not also record agent-failure, got %d", got)
	}
	if r.calls != 1 {
		t.Errorf("a deadline must fail fast (1 attempt), got %d", r.calls)
	}
}

// The summariser (a mandatory step node) records the same structured retry/
// failure telemetry as a reviewer — closing AC2 over the summariser path too.
func TestSummarise_recordsRetryAndFailureTelemetry(t *testing.T) {
	docs := fakeDocs{workflow: summaryWorkflow, skillBody: "summarise rubric", skillFound: true}
	r := &scriptedRunner{results: []struct {
		out string
		err error
	}{
		{err: errFakeAgent}, // transient run error → agent-retry outcome=error
		{out: "   "},        // empty output        → agent-retry outcome=empty-output
	}}
	g := New(r, docs, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }
	recs := captureTelemetry(g)

	if _, err := g.Summarise(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "in_progress", "done"); err == nil {
		t.Fatal("a mandatory summary that never succeeds should surface an error")
	}
	if got := countKind(*recs, "agent-retry"); got != defaultReviewerAttempts {
		t.Errorf("want %d summariser agent-retry events, got %d", defaultReviewerAttempts, got)
	}
	if got := countKind(*recs, "agent-failure"); got != 1 {
		t.Errorf("want 1 terminal summariser agent-failure, got %d", got)
	}
	if !hasOutcome(*recs, "agent-retry", "empty-output") {
		t.Error("an empty summary attempt should record outcome=empty-output")
	}
	for _, rec := range *recs {
		if s, _ := rec.data["skill"].(string); s != summariserSkill {
			t.Errorf("summariser telemetry skill = %q, want %q", s, summariserSkill)
		}
	}
}

// A named binding's own timeout bounds its dispatch (sty_446c38b7): a 1ms bound on
// a runner that blocks until cancelled surfaces DeadlineExceeded fast — proof the
// binding's timeout is applied, not the engine's 20m default (which would hang the
// test). The default-when-unset path is covered by config.TestTimeoutDuration.
func TestDispatchExecutor_honorsBindingTimeout(t *testing.T) {
	docs := fakeDocs{workflow: dispatchWF, skillBody: "alignment rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name != "architect" {
			return config.AgentBinding{}, false
		}
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Grep,Glob,Bash(satelle:*)", Model: "fable", Timeout: "1ms"}, true
	})
	g.newRunner = func(string, string) (agentcli.Runner, error) { return &blockingRunner{}, nil }

	done := make(chan error, 1)
	go func() {
		_, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "plan")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
			t.Fatalf("want a deadline-exceeded dispatch failure from the 1ms binding timeout, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the binding's 1ms timeout was not applied — dispatch did not return (fell back to the long default)")
	}
}

// A named executor whose dispatch runner errors records a structured
// agent-failure naming the agent, step, and classified outcome (AC2) — the
// coded capture of a killed sub-process only the binary observes.
func TestDispatchExecutor_recordsFailureTelemetry(t *testing.T) {
	docs := fakeDocs{workflow: dispatchWF, skillBody: "alignment rubric", skillFound: true}
	g, _ := newEngine(t, "", docs)
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name != "architect" {
			return config.AgentBinding{}, false
		}
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read,Grep,Glob,Bash(satelle:*)", Model: "fable"}, true
	})
	g.newRunner = func(string, string) (agentcli.Runner, error) {
		return &fakeRunner{err: errors.New("agentcli: claude: signal: killed")}, nil
	}
	recs := captureTelemetry(g)

	if _, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "plan"); err == nil {
		t.Fatal("dispatch should fail when the binding runner errors")
	}
	if got := countKind(*recs, "agent-failure"); got != 1 {
		t.Fatalf("want 1 executor agent-failure event, got %d", got)
	}
	rec := (*recs)[0]
	if rec.actor != "executor" {
		t.Errorf("executor telemetry actor = %q, want executor", rec.actor)
	}
	if a, _ := rec.data["agent"].(string); a != "architect" {
		t.Errorf("telemetry agent = %q, want architect", a)
	}
	if s, _ := rec.data["step"].(string); s != "plan" {
		t.Errorf("telemetry step = %q, want plan", s)
	}
	if o, _ := rec.data["outcome"].(string); o != "signal:killed" {
		t.Errorf("telemetry outcome = %q, want signal:killed", o)
	}
}

func TestGate_inLoopReviewerFailsLoud(t *testing.T) {
	g, _ := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: "rubric body", skillFound: true})
	g.SetReviewerBinding(config.AgentBinding{Command: "in-loop", Role: config.RoleReviewer, Principles: config.PrinciplesNone})
	g.SetRunner(nil) // force re-check; Invoke also needs runner, but we fail earlier
	// Put a dummy runner so we hit the in-loop check before nil-runner.
	g.SetRunner(&fakeRunner{out: `{"decision":"accept"}`})
	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_x", Status: "backlog"}, "in_progress")
	if err == nil {
		t.Fatalf("expected in-loop reviewer to fail, got accept=%v", dec.Accept)
	}
	if !strings.Contains(err.Error(), "in-loop") || !strings.Contains(err.Error(), "isolated verdict") {
		t.Errorf("error should name in-loop mechanism failure: %v", err)
	}
}

func TestIsNamedPerformer(t *testing.T) {
	if isNamedPerformer("planner", config.AgentBinding{Role: config.RoleAgent}) != true {
		t.Error("planner role=agent should perform")
	}
	if isNamedPerformer("strict", config.AgentBinding{Role: config.RoleReviewer}) != false {
		t.Error("named role=reviewer must not perform")
	}
	if isNamedPerformer("reviewer", config.AgentBinding{}) != false {
		t.Error("DSL reviewer token must not perform")
	}
}

// parkWF declares from="*" on blocked so parking works from every performing
// state; no resume edges — resume is origin-enforced (sty_f75286dc).
var parkWF = wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  integration [agent=executor, prompt="@skill:integrate"]
  release [agent=executor, prompt="@skill:release"]
  done [shape=Msquare]
  cancelled [agent=reviewer, prompt="@skill:cancel"]
  blocked [agent=reviewer, prompt="@skill:park-gate", from="*"]
  backlog -> in_progress -> integration -> release -> done
  blocked -> cancelled
}`)

// TestGateParkResumeToOrigin (sty_f75286dc): parked from integration resumes
// only to integration (ungated); release is refused — no gate wormhole.
func TestGateParkResumeToOrigin(t *testing.T) {
	g := New(&fakeRunner{}, fakeDocs{workflow: parkWF, skillFound: false}, "/repo", "")
	item := workitem.Item{
		ID: "sty_park", Status: "blocked", ParkOrigin: "integration",
		Kind: workitem.KindStory, Tags: []string{},
	}
	// Resume to origin: ungated accept.
	dec, err := g.Gate(context.Background(), item, "integration")
	if err != nil {
		t.Fatalf("resume to origin: %v", err)
	}
	if dec.Gated {
		t.Error("resume must be ungated (no re-run of gates)")
	}
	// Wormhole to release: refused.
	_, err = g.Gate(context.Background(), item, "release")
	if err == nil {
		t.Fatal("expected refuse park→release")
	}
	if !strings.Contains(err.Error(), "origin") {
		t.Errorf("error should name origin, got: %v", err)
	}
	// Cancel still allowed (declared non-performing exit).
	_, err = g.Gate(context.Background(), item, "cancelled")
	if err != nil {
		t.Fatalf("park→cancelled should be allowed: %v", err)
	}
}

// TestGateParkFromIntegration (sty_f75286dc): parking from integration is a
// declared edge (materialized from from=*) — not "not a declared edge".
func TestGateParkFromIntegration(t *testing.T) {
	g := New(&fakeRunner{}, fakeDocs{workflow: parkWF, skillFound: false}, "/repo", "")
	item := workitem.Item{
		ID: "sty_park2", Status: "integration",
		Kind: workitem.KindStory, Tags: []string{},
	}
	_, err := g.Gate(context.Background(), item, "blocked")
	if err != nil {
		t.Fatalf("integration→blocked should be declared: %v", err)
	}
}

// --- parallel multi-reviewer gates (sty_4f0a15db) ---

func parallelWF(parallelAttr string) string {
	edge := `backlog -> in_progress [agent=reviewer, prompt="@skill:rev-a,@skill:rev-b"`
	if parallelAttr != "" {
		edge += `, parallel=` + parallelAttr
	}
	edge += `]`
	return wfDoc(baselineWorkflow, `"*"`, `
  rankdir=LR
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare]
  `+edge+`
  in_progress -> done
`)
}

func parallelSkill(name string) string {
	return "---\nname: " + name + "\nscope: system\ntype: skill\ntags: [type:skill, type:reviewer]\ndescription: parallel test gate\n---\n\n# " + name + "\n\nReturn a verdict:\n\n```json\n{\"decision\": \"accept\", \"notes\": \"\"}\n```\n"
}

// concurrentMapRunner records max concurrency via short sleep; returns per-skill verdicts.
type concurrentMapRunner struct {
	mu       sync.Mutex
	inflight int
	maxConc  int
	verdict  map[string]string
	calls    map[string]int
	delay    time.Duration
}

func newConcurrentMapRunner(verdict map[string]string) *concurrentMapRunner {
	return &concurrentMapRunner{verdict: verdict, calls: map[string]int{}, delay: 30 * time.Millisecond}
}

func (r *concurrentMapRunner) Name() string    { return "conc" }
func (r *concurrentMapRunner) Command() string { return "conc" }
func (r *concurrentMapRunner) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	sk := "unknown"
	for _, name := range []string{"rev-a", "rev-b", "rev-c"} {
		if strings.Contains(req.SystemPrompt, name) {
			sk = name
			break
		}
	}
	r.mu.Lock()
	r.calls[sk]++
	r.inflight++
	if r.inflight > r.maxConc {
		r.maxConc = r.inflight
	}
	r.mu.Unlock()
	time.Sleep(r.delay)
	r.mu.Lock()
	r.inflight--
	dec := r.verdict[sk]
	if dec == "" {
		dec = "accept"
	}
	r.mu.Unlock()
	return []byte(`{"decision":"` + dec + `","notes":"` + sk + `"}`), nil
}

func TestGateParallel_CollectsAllNoShortCircuit(t *testing.T) {
	docs := fakeDocs{
		workflow: parallelWF("true"),
		extraSkills: []docindex.Doc{
			{Kind: "skills", Name: "rev-a", Body: parallelSkill("rev-a")},
			{Kind: "skills", Name: "rev-b", Body: parallelSkill("rev-b")},
		},
	}
	runner := newConcurrentMapRunner(map[string]string{"rev-b": "reject"})
	g := New(runner, docs, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }

	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_p", Status: "backlog", Category: "feature"}, "in_progress")
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if len(dec.Reviewers) != 2 {
		t.Fatalf("parallel must collect all N verdicts, got %d: %+v", len(dec.Reviewers), dec.Reviewers)
	}
	if dec.Reviewers[0].Order != 0 || dec.Reviewers[1].Order != 1 {
		t.Errorf("deterministic order broken: %+v", dec.Reviewers)
	}
	if dec.Reviewers[0].Skill != "rev-a" || dec.Reviewers[1].Skill != "rev-b" {
		t.Errorf("skills order: %+v", dec.Reviewers)
	}
	if dec.Reviewers[1].Accept {
		t.Error("rev-b should reject")
	}
	runner.mu.Lock()
	maxc := runner.maxConc
	callsA, callsB := runner.calls["rev-a"], runner.calls["rev-b"]
	runner.mu.Unlock()
	if callsA != 1 || callsB != 1 {
		t.Errorf("each skill once: a=%d b=%d", callsA, callsB)
	}
	if maxc < 2 {
		t.Errorf("expected concurrent in-flight ≥2, got maxConc=%d", maxc)
	}
}

func TestGateSerial_ShortCircuitFirstReject(t *testing.T) {
	docs := fakeDocs{
		workflow: parallelWF(""), // no parallel attr
		extraSkills: []docindex.Doc{
			{Kind: "skills", Name: "rev-a", Body: parallelSkill("rev-a")},
			{Kind: "skills", Name: "rev-b", Body: parallelSkill("rev-b")},
		},
	}
	runner := newConcurrentMapRunner(map[string]string{"rev-a": "reject"})
	g := New(runner, docs, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }

	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_s", Status: "backlog", Category: "feature"}, "in_progress")
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if len(dec.Reviewers) != 1 {
		t.Fatalf("serial short-circuit should stop after first reject, got %d", len(dec.Reviewers))
	}
	if dec.Reviewers[0].Accept {
		t.Error("expected reject")
	}
	runner.mu.Lock()
	callsB := runner.calls["rev-b"]
	runner.mu.Unlock()
	if callsB != 0 {
		t.Errorf("rev-b must not run after rev-a reject, calls=%d", callsB)
	}
}

func TestReviewerSkillsFor_Parallel(t *testing.T) {
	body := parallelWF("true")
	skills, _, par, declared := reviewerSkillsFor(body, "backlog", "in_progress")
	if !declared || len(skills) != 2 {
		t.Fatalf("skills=%v declared=%v", skills, declared)
	}
	if par != wfdot.DefaultParallelCap {
		t.Errorf("parallel=true → cap %d, want %d", par, wfdot.DefaultParallelCap)
	}
	body2 := parallelWF("2")
	_, _, par2, _ := reviewerSkillsFor(body2, "backlog", "in_progress")
	if par2 != 2 {
		t.Errorf("parallel=2 → %d", par2)
	}
	body3 := parallelWF("")
	_, _, par3, _ := reviewerSkillsFor(body3, "backlog", "in_progress")
	if par3 != 0 {
		t.Errorf("absent → %d", par3)
	}
}

func TestGateRefusesPerformerRoleOnEdge(t *testing.T) {
	wf := wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog [shape=Mdiamond]
  done [shape=Msquare]
  backlog -> done [agent=coder-x, prompt="@skill:rev-a"]
}`)
	g, _ := newEngine(t, `{"decision":"accept","notes":"ok"}`, fakeDocs{
		workflow: wf, skillBody: "rubric", skillFound: true,
	})
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name == "coder-x" {
			return config.AgentBinding{Command: agentcli.DefaultClaudeCommand, Tools: "Read", Role: "agent"}, true
		}
		return config.AgentBinding{}, false
	})
	_, err := g.Gate(context.Background(), workitem.Item{ID: "sty_x", Status: "backlog"}, "done")
	if err == nil || !strings.Contains(err.Error(), "role=") {
		t.Fatalf("want role refusal, got %v", err)
	}
}

func TestDispatchRefusesReviewerRoleOnPerformingNode(t *testing.T) {
	wf := wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog [shape=Mdiamond]
  work [agent=reviewer-deep, prompt="@skill:code"]
  done [shape=Msquare]
  backlog -> work -> done
}`)
	g, r := newEngine(t, `{"decision":"accept","notes":"ok"}`, fakeDocs{
		workflow: wf, skillBody: "rubric", skillFound: true,
	})
	called := false
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name == "reviewer-deep" {
			return config.AgentBinding{Command: agentcli.DefaultClaudeCommand, Tools: "Read", Role: "reviewer", Model: "opus"}, true
		}
		return config.AgentBinding{}, false
	})
	// Replace runner to detect invoke
	orig := r
	_ = orig
	_, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_x", Status: "backlog"}, "work")
	if err == nil || !strings.Contains(err.Error(), "role=reviewer") {
		t.Fatalf("want reviewer-on-performing refuse, got %v", err)
	}
	_ = called
}

// TestGateBindingEmptyDegradesToReviewer pins sty_4cebc624 AC4: an omitted
// agent= on a gate (Agent == "") resolves to [reviewer] rather than erroring.
func TestGateBindingEmptyDegradesToReviewer(t *testing.T) {
	g := New(&fakeRunner{}, fakeDocs{}, "/repo", "opus")
	g.reviewerBinding = config.AgentBinding{Command: "claude", Model: "opus"}
	for _, sec := range []string{"", "reviewer", "  "} {
		b, name, err := g.gateBinding(sec)
		if err != nil {
			t.Fatalf("gateBinding(%q) err: %v", sec, err)
		}
		if name != "reviewer" {
			t.Errorf("gateBinding(%q) name=%q, want reviewer", sec, name)
		}
		if b.Model != "opus" {
			t.Errorf("gateBinding(%q) model=%q, want opus", sec, b.Model)
		}
	}
}

// TestCodedCheckScopedGateAgentNeutral pins sty_4cebc624 AC4: a coded-check
// scoped on= node with agent= and the same node without agent= produce
// byte-identical GateDecision JSON for both accept and reject check scripts.
// The early return before gateBinding means agent= is never read.
func TestCodedCheckScopedGateAgentNeutral(t *testing.T) {
	codedSkill := `---
name: fixture-coded
type: skill
description: coded check fixture
---

# Fixture

` + "```check\n#!/bin/sh\nexit 0\n```\n"

	mkWF := func(agentAttr string) string {
		// agentAttr is either `agent=reviewer, ` or empty.
		return wfDoc(baselineWorkflow, `"*"`, `digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done [shape=Msquare]
  codecheck [`+agentAttr+`prompt="@skill:fixture-coded", on="done"]
  backlog -> in_progress
  in_progress -> done
}`)
	}

	run := func(t *testing.T, wf string, checkOK bool) verb.GateDecision {
		t.Helper()
		docs := fakeDocs{
			workflow:   wf,
			skillFound: true,
			extraSkills: []docindex.Doc{
				{Kind: "skills", Name: "fixture-coded", Body: codedSkill},
				{Kind: "skills", Name: "code", Body: conformantSkill("code", "implement")},
			},
		}
		g, r := newEngine(t, `{"decision":"accept"}`, docs)
		g.check = func(_ context.Context, dir, command, payload string) (string, error) {
			if checkOK {
				return "ok\n", nil
			}
			return "FAIL\n", errFakeExit
		}
		dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_t", Status: "in_progress"}, "done")
		if err != nil {
			t.Fatal(err)
		}
		if r.got.SystemPrompt != "" {
			t.Error("LLM must not run for coded-check gate")
		}
		return dec
	}

	for _, ok := range []bool{true, false} {
		name := "accept"
		if !ok {
			name = "reject"
		}
		t.Run(name, func(t *testing.T) {
			with := run(t, mkWF(`agent=reviewer, `), ok)
			without := run(t, mkWF(``), ok)
			if with.Accept != without.Accept || with.Gated != without.Gated {
				t.Fatalf("accept/gated differ: with=%+v without=%+v", with, without)
			}
			// Notes for functional checks include the command / output; require equal.
			bw, err := json.Marshal(with)
			if err != nil {
				t.Fatal(err)
			}
			bo, err := json.Marshal(without)
			if err != nil {
				t.Fatal(err)
			}
			if string(bw) != string(bo) {
				t.Fatalf("GateDecision not byte-identical:\nwith:    %s\nwithout: %s", bw, bo)
			}
			if ok && !with.Accept {
				t.Fatal("want accept")
			}
			if !ok && with.Accept {
				t.Fatal("want reject")
			}
		})
	}
}
