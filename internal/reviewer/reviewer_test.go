package reviewer

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const testWorkflow = `
transitions:
  - {from: backlog, to: in_progress, reviewer_skill: "satelle-intent-plan-review"}
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
}

func (d fakeDocs) Get(_ context.Context, kind, name string) (docindex.Doc, error) {
	switch kind {
	case "workflows":
		if name == baselineWorkflow {
			return docindex.Doc{Kind: kind, Name: name, Body: d.workflow}, nil
		}
	case "skills":
		if d.skillFound {
			return docindex.Doc{Kind: kind, Name: name, Body: d.skillBody}, nil
		}
		return docindex.Doc{}, docindex.ErrNotFound
	}
	return docindex.Doc{}, docindex.ErrNotFound
}

func (d fakeDocs) List(_ context.Context, kind string) ([]docindex.Doc, error) {
	if kind != "workflows" {
		return nil, nil
	}
	out := []docindex.Doc{{Kind: "workflows", Name: baselineWorkflow, Body: d.workflow}}
	return append(out, d.extraWorkflows...), nil
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

func TestReviewerSkillFor(t *testing.T) {
	if got := reviewerSkillFor(testWorkflow, "in_progress", "done"); got != "satelle-story-done-review" {
		t.Errorf("in_progress→done skill = %q", got)
	}
	if got := reviewerSkillFor(testWorkflow, "backlog", "cancelled"); got != "" {
		t.Errorf("ungated edge skill = %q, want empty", got)
	}
	if got := reviewerSkillFor(testWorkflow, "backlog", "nowhere"); got != "" {
		t.Errorf("unknown edge skill = %q, want empty", got)
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

const checkSkill = "---\nname: satelle-integration-review\nkind: skill\ncheck: \"run-the-suite\"\n---\n# Integration gate\nRuns the suite.\n"

func TestFunctionalCheckGate(t *testing.T) {
	// A workflow edge whose reviewer skill carries a `check:` runs deterministically.
	wf := "transitions:\n  - {from: in_progress, to: integrated, reviewer_skill: \"satelle-integration-review\"}\n"

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
