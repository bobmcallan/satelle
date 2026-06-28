package reviewer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
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

func (f *fakeRunner) Name() string { return "fake" }
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
}

func (d fakeDocs) Get(_ context.Context, kind, name string) (docindex.Doc, error) {
	switch kind {
	case "workflows":
		if name == baselineWorkflow {
			return docindex.Doc{Kind: kind, Name: name, Body: d.workflow}, nil
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
	}
	return nil, nil
}

func gater(t *testing.T, out string, docs fakeDocs) (*Gater, *fakeRunner) {
	t.Helper()
	r := &fakeRunner{out: out}
	return New(r, docs, "/repo", ""), r
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
	if r.got.SystemPrompt != "rubric body" {
		t.Errorf("skill body should ride as the system prompt, got %q", r.got.SystemPrompt)
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

func (m *mapRunner) Name() string { return "map" }
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

func TestGateSystemReviewerRunsLast(t *testing.T) {
	// An always-on system reviewer (tagged reviewer:always) runs AFTER the
	// workflow-named reviewer — last in order, flagged System.
	sysSkill := "---\nname: satelle-estimate-actual\nkind: skill\ntags: [kind:skill, reviewer:always]\n---\n# always-on\nrecord estimate/actual\n"
	mr := &mapRunner{}
	docs := fakeDocs{
		workflow:   testWorkflow, // in_progress→done is gated by satelle-story-done-review
		skillBody:  "rubric",
		skillFound: true,
		extraSkills: []docindex.Doc{
			{Kind: "skills", Name: "satelle-estimate-actual", Body: sysSkill},
		},
	}
	g := New(mr, docs, "/repo", "")
	dec, err := g.Gate(context.Background(), workitem.Item{Status: "in_progress"}, "done")
	if err != nil {
		t.Fatal(err)
	}
	if len(dec.Reviewers) != 2 {
		t.Fatalf("want 2 reviewers (named + system), got %+v", dec.Reviewers)
	}
	first, last := dec.Reviewers[0], dec.Reviewers[1]
	if first.Skill != "satelle-story-done-review" || first.System {
		t.Errorf("first reviewer should be the workflow-named one, got %+v", first)
	}
	if last.Skill != "satelle-estimate-actual" || !last.System || last.Order != 1 {
		t.Errorf("system reviewer should run last and be flagged System, got %+v", last)
	}
}

func TestSystemReviewerScopedByOnList(t *testing.T) {
	// An always-on reviewer that scopes itself with `on: [done]` joins the close
	// edge but is skipped on an unlisted edge — so it costs nothing in between.
	scoped := "---\nname: satelle-estimate-actual-review\nkind: skill\ntags: [kind:skill, reviewer:always]\non: [done]\n---\n# scoped\nrubric\n"
	docs := func() fakeDocs {
		return fakeDocs{
			workflow:   "transitions:\n  - {from: in_progress, to: reviewed, reviewer_skill: \"satelle-story-code-review\"}\n  - {from: deployed, to: done, reviewer_skill: \"satelle-story-done-review\"}\n",
			skillBody:  "rubric",
			skillFound: true,
			extraSkills: []docindex.Doc{
				{Kind: "skills", Name: "satelle-estimate-actual-review", Body: scoped},
			},
		}
	}

	// to=reviewed is NOT in the scoped reviewer's on-list → only the named reviewer runs.
	g1 := New(&mapRunner{}, docs(), "/repo", "")
	dec1, err := g1.Gate(context.Background(), workitem.Item{Status: "in_progress"}, "reviewed")
	if err != nil {
		t.Fatal(err)
	}
	if len(dec1.Reviewers) != 1 || dec1.Reviewers[0].Skill != "satelle-story-code-review" {
		t.Fatalf("reviewed edge should run only the named reviewer, got %+v", dec1.Reviewers)
	}

	// to=done IS in the on-list → the scoped system reviewer joins, last.
	g2 := New(&mapRunner{}, docs(), "/repo", "")
	dec2, err := g2.Gate(context.Background(), workitem.Item{Status: "deployed"}, "done")
	if err != nil {
		t.Fatal(err)
	}
	if len(dec2.Reviewers) != 2 || !dec2.Reviewers[1].System || dec2.Reviewers[1].Skill != "satelle-estimate-actual-review" {
		t.Fatalf("done edge should add the scoped system reviewer last, got %+v", dec2.Reviewers)
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

func TestReviewCreateAcceptAndReject(t *testing.T) {
	ctx := context.Background()
	draft := verb.CreateDraft{Kind: "story", Title: "x", AcceptanceCriteria: "1. a"}

	g, r := gater(t, `the draft is well-formed {"decision":"accept","notes":""}`,
		fakeDocs{skillBody: "structure rubric", skillFound: true})
	dec, err := g.ReviewCreate(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Gated || !dec.Accept || dec.Skill != structureSkill {
		t.Fatalf("want gated accept by %s, got %+v", structureSkill, dec)
	}
	if r.got.SystemPrompt != "structure rubric" {
		t.Errorf("structure rubric should be the system prompt, got %q", r.got.SystemPrompt)
	}

	g2, _ := gater(t, `{"decision":"reject","notes":"add numbered acceptance criteria"}`,
		fakeDocs{skillBody: "rubric", skillFound: true})
	dec2, err := g2.ReviewCreate(ctx, verb.CreateDraft{Kind: "story", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !dec2.Gated || dec2.Accept || dec2.Notes == "" {
		t.Fatalf("want gated reject with notes, got %+v", dec2)
	}
}

func TestReviewCreateAdvisoryWhenRubricAbsent(t *testing.T) {
	g, r := gater(t, `{"decision":"reject"}`, fakeDocs{skillFound: false})
	dec, err := g.ReviewCreate(context.Background(), verb.CreateDraft{Kind: "story", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Gated {
		t.Errorf("absent structure rubric should be advisory, got %+v", dec)
	}
	if r.got.SystemPrompt != "" {
		t.Errorf("reviewer must not run without a rubric")
	}
}

func TestSummariseReturnsTrimmedProse(t *testing.T) {
	g, r := gater(t, "  Moved from in_progress to done after the criteria were met.\n",
		fakeDocs{skillBody: "summariser rubric", skillFound: true})
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
	// Read-only grant — the summariser must not be able to mutate the tree.
	for _, banned := range []string{"Write", "Edit", "Bash"} {
		if contains(r.got.AllowedTools, banned) {
			t.Errorf("summariser tool grant %q should be read-only", r.got.AllowedTools)
		}
	}
}

func TestSummariseEmptyWhenRubricAbsent(t *testing.T) {
	g, r := gater(t, "should not run", fakeDocs{skillFound: false})
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

func names(ds []docindex.Doc) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Name
	}
	return out
}

func TestStructureReviewerFor(t *testing.T) {
	cases := map[string]string{
		"skills":     "satelle-skill-review",
		"workflows":  "satelle-workflow-review",
		"principles": "satelle-principle-review",
		"documents":  "",
		"":           "",
	}
	for kind, want := range cases {
		if got := StructureReviewerFor(kind); got != want {
			t.Errorf("StructureReviewerFor(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestReviewStructureAcceptReject(t *testing.T) {
	ctx := context.Background()
	g, r := gater(t, `{"decision":"accept","notes":""}`, fakeDocs{skillBody: "skill rubric", skillFound: true})
	dec, err := g.ReviewStructure(ctx, "satelle-skill-review", "skills", "x", "---\nkind: skill\n---\nbody")
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Gated || !dec.Accept || dec.Skill != "satelle-skill-review" {
		t.Fatalf("want gated accept by satelle-skill-review, got %+v", dec)
	}
	if r.got.SystemPrompt != "skill rubric" {
		t.Errorf("reviewer rubric should be the system prompt, got %q", r.got.SystemPrompt)
	}

	g2, _ := gater(t, `{"decision":"reject","notes":"missing kind"}`, fakeDocs{skillBody: "rubric", skillFound: true})
	dec2, _ := g2.ReviewStructure(ctx, "satelle-workflow-review", "workflows", "w", "body")
	if !dec2.Gated || dec2.Accept || dec2.Notes == "" {
		t.Fatalf("want gated reject with notes, got %+v", dec2)
	}

	// Advisory when the rubric is absent.
	g3, _ := gater(t, ``, fakeDocs{skillFound: false})
	dec3, _ := g3.ReviewStructure(ctx, "satelle-skill-review", "skills", "x", "body")
	if dec3.Gated {
		t.Errorf("absent rubric should be advisory, got %+v", dec3)
	}
}

const dotWF = `---
name: x
---
# w

` + "```dot" + `
digraph w {
  in_progress [actor=executor]
  committed   [actor=reviewer, prompt="@skill:satelle-commit-push-reviewer"]
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
