package agentstep

import (
	"context"
	"reflect"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_73224b86: how a re-presented performer's output stands against the
// blocking findings it was offered — answered, changed, or neither.

func quoted(id string, quotes ...string) Finding {
	return Finding{ID: id, Decision: "reject", Blocking: true, quotes: quotes}
}

func TestComputeProgress(t *testing.T) {
	material := func(judged string) ProgressMaterial { return ProgressMaterial{Judged: judged} }
	cases := []struct {
		name        string
		offered     []Finding
		answers     []agentartifact.Answer
		m           ProgressMaterial
		progressed  []string
		unprogessed []string
	}{
		{"quotation still present, re-flowed", []Finding{quoted("a#1", "one  two\nthree")}, nil, material("x one two three y"), nil, []string{"a#1"}},
		{"quotation gone", []Finding{quoted("a#1", "one two")}, nil, material("one changed"), []string{"a#1"}, nil},
		{"seat quotation kept, criterion quotation gone", []Finding{quoted("a#1", "seat text", "criterion text")}, nil, material("seat text only"), []string{"a#1"}, nil},
		{"every quotation kept", []Finding{quoted("a#1", "seat text", "criterion text")}, nil, material("criterion text and seat text"), nil, []string{"a#1"}},
		{"answered, quotation kept", []Finding{quoted("a#1", "kept")}, []agentartifact.Answer{{Finding: "a#1", Answer: agentartifact.AnswerRebutted}}, material("kept"), []string{"a#1"}, nil},
		{"answer to a finding not offered counts for nothing", []Finding{quoted("a#1", "kept")}, []agentartifact.Answer{{Finding: "zzz#9", Answer: agentartifact.AnswerAdopted}}, material("kept"), nil, []string{"a#1"}},
		{"no quotation, output identical", []Finding{quoted("a#1")}, nil, ProgressMaterial{Judged: "j", Output: "same", Prior: "same", HasOutput: true}, nil, []string{"a#1"}},
		{"no quotation, output differs", []Finding{quoted("a#1")}, nil, ProgressMaterial{Judged: "j", Output: "new", Prior: "old", HasOutput: true}, []string{"a#1"}, nil},
		{"no quotation, no output document is never a change", []Finding{quoted("a#1")}, nil, ProgressMaterial{Judged: "j", Prior: "old"}, nil, []string{"a#1"}},
		{"an accept is not a finding to answer", []Finding{{ID: "ok#1", Decision: "accept"}, quoted("a#1", "gone")}, nil, material("other"), []string{"a#1"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := computeProgress(c.offered, c.answers, c.m)
			if p == nil {
				t.Fatal("progress = nil, want a fact")
			}
			if !reflect.DeepEqual(p.Progressed, c.progressed) || !reflect.DeepEqual(p.Unprogressed, c.unprogessed) {
				t.Errorf("progressed = %v unprogressed = %v, want %v and %v", p.Progressed, p.Unprogressed, c.progressed, c.unprogessed)
			}
		})
	}
	if p := computeProgress([]Finding{{ID: "ok#1", Decision: "accept"}}, nil, ProgressMaterial{}); p != nil {
		t.Errorf("nothing blocking was offered, progress = %+v, want none", p)
	}
}

func TestFindingQuotesAreOnlyTheCompleteOnes(t *testing.T) {
	v := PriorVerdict{Reviewed: "seat", Criteria: []verb.CriterionVerdict{
		{Criterion: 1, Status: verb.CriterionRejected, Reviewed: "rejected one"},
		{Criterion: 2, Status: verb.CriterionAccepted, Reviewed: "accepted two"},
		{Criterion: 3, Status: verb.CriterionRejected, Reviewed: "cut", ReviewedTruncated: true},
	}}
	if got := findingQuotes(v); !reflect.DeepEqual(got, []string{"seat", "rejected one"}) {
		t.Errorf("quotes = %v, want the seat's and the rejected criterion's complete ones", got)
	}
	for name, v := range map[string]PriorVerdict{
		"unavailable": {Reviewed: "seat", Unavailable: true},
		"stale":       {Reviewed: "seat", ReviewedStale: true},
		"truncated":   {Reviewed: "seat", ReviewedTruncated: true},
		"empty":       {Reviewed: "  "},
	} {
		if got := findingQuotes(v); got != nil {
			t.Errorf("%s: quotes = %v, want none", name, got)
		}
	}
}

// progressDispatch dispatches the plan step against a two-seat panel prior whose
// seats quote the prior output document, and returns the progress fact. The
// skill decides which tail of DispatchExecutor resolves the output: an attempt
// policy, or a plain contract.
func progressDispatch(t *testing.T, attempts bool, newBody, answers string) *verb.FindingProgress {
	t.Helper()
	const priorBody = "## AC1\nold one\n## AC2\nold two"
	artifact := validAttempt(newBody)
	if answers != "" {
		artifact = artifact[:len(artifact)-1] + `,"answers":` + answers + "}"
	}
	prior := priorResolver(PriorVerdict{Skill: "arch", Decision: "reject", Notes: "combined", Seats: []verb.PanelSeat{
		{Seat: "opus", Notes: "AC2 is wrong", Reviewed: "old two"},
		{Seat: "grok", Notes: "AC1 is wrong", Reviewed: "old one"},
	}})
	docs := func(context.Context, string) []DocState {
		return []DocState{{Name: "design", Type: "design-note", Body: priorBody}}
	}
	var g *Engine
	if attempts {
		g, _ = attemptedEngine(t, attemptedDispatchSkill, &attemptRunner{runs: []attemptRun{{out: artifact}}}, &attemptRunner{})
	} else {
		g, _ = newEngine(t, "", fakeDocs{workflow: dispatchWF, skillBody: contractedDispatchSkill, skillFound: true})
		g.newRunner = func(string, string) (agentcli.Runner, error) { return &fakeRunner{out: artifact}, nil }
		g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
			return config.AgentBinding{Command: "fake -p {system}", Tools: "read_file,grep,list_dir"}, true
		})
	}
	g.SetPriorVerdictsResolver(prior)
	g.SetDocsResolver(docs)
	g.SetArtifactAttacher(func(_ context.Context, _ workitem.Item, name, typ, _ string) (string, string, error) {
		return name, typ, nil
	})
	res, err := g.DispatchExecutor(context.Background(), attemptedItem(), "plan")
	if err != nil {
		t.Fatal(err)
	}
	return res.Progress
}

// The output is final on BOTH branches of the dispatch tail — a plain contract
// and an attempt policy — so a changed quotation reaches the verb on each.
func TestDispatchReportsProgressOnEveryOutputBranch(t *testing.T) {
	for name, attempts := range map[string]bool{"contract without attempts": false, "attempt policy": true} {
		t.Run(name, func(t *testing.T) {
			p := progressDispatch(t, attempts, "## AC1\nold one\n## AC2\nnew two", "")
			if p == nil || !reflect.DeepEqual(p.Offered, []string{"arch/opus#1", "arch/grok#1"}) {
				t.Fatalf("progress = %+v, want both seats offered", p)
			}
			if !reflect.DeepEqual(p.Progressed, []string{"arch/opus#1"}) || !reflect.DeepEqual(p.Unprogressed, []string{"arch/grok#1"}) {
				t.Errorf("only the seat whose quotation was removed progressed: %+v", p)
			}

			same := progressDispatch(t, attempts, "## AC1\nold one\n## AC2\nold two", "")
			if len(same.Progressed) != 0 || len(same.Unprogressed) != 2 {
				t.Errorf("the same output changes nothing: %+v", same)
			}

			answered := progressDispatch(t, attempts, "## AC1\nold one\n## AC2\nold two",
				`[{"finding":"arch/grok#1","answer":"rebutted","evidence":"AC1 is right"}]`)
			if !reflect.DeepEqual(answered.Progressed, []string{"arch/grok#1"}) {
				t.Errorf("an answered finding progressed even though its quotation is kept: %+v", answered)
			}
		})
	}
}
