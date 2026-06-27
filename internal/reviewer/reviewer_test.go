package reviewer

import (
	"context"
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

func TestParseDecisionStrict(t *testing.T) {
	for _, in := range []string{`{"decision":"maybe"}`, `{"notes":"x"}`, ``} {
		if _, err := parseDecision([]byte(in)); err == nil {
			t.Errorf("parseDecision(%q) should error", in)
		}
	}
}
