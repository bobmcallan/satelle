package agentstep

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestGateRecordsUnresolvedSkill (sty_d59ec6a9 AC5): when an edge declares a
// gate whose rubric is absent, the decision names the skill that was skipped.
// Fail-open is unchanged — Gated is still false and the transition still
// advances — but the advance is no longer indistinguishable from an edge that
// never carried a gate.
func TestGateRecordsUnresolvedSkill(t *testing.T) {
	g, r := newEngine(t, `{"decision":"reject"}`,
		fakeDocs{workflow: testWorkflow, skillFound: false})

	dec, err := g.Gate(context.Background(), workitem.Item{Status: "in_progress"}, "done")
	if err != nil {
		t.Fatal(err)
	}
	if dec.Gated {
		t.Errorf("fail-open must be preserved: absent rubric stays advisory, got %+v", dec)
	}
	if r.got.SystemPrompt != "" {
		t.Error("no reviewer may run without a rubric")
	}
	if len(dec.Unresolved) != 1 || dec.Unresolved[0] != "satelle-story-done-review" {
		t.Errorf("Unresolved = %v, want the declared-but-absent skill", dec.Unresolved)
	}
}

// TestGateLeavesUnresolvedEmptyWhenGateResolves: the field is evidence of an
// ungated advance, so a normally judged edge must not carry it — otherwise
// every accept would look like a skip.
func TestGateLeavesUnresolvedEmptyWhenGateResolves(t *testing.T) {
	g, _ := newEngine(t, `{"decision":"accept"}`,
		fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})

	dec, err := g.Gate(context.Background(), workitem.Item{Status: "in_progress"}, "done")
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Gated || !dec.Accept {
		t.Fatalf("expected a normal accept, got %+v", dec)
	}
	if len(dec.Unresolved) != 0 {
		t.Errorf("a judged edge must carry no Unresolved, got %v", dec.Unresolved)
	}
}

// TestWorkflowSkillProblemsIsPerDocAndExcludesAmbiguity (AC3): the extracted
// per-workflow check reports unresolved skills, and must NOT report the
// ambiguity problem — that compares repo workflows against each other, so it is
// whole-set by nature and firing it per-doc would misreport.
func TestWorkflowSkillProblemsIsPerDocAndExcludesAmbiguity(t *testing.T) {
	// A lifecycle names its gates in the route grammar (sty_d953c5d8), so the
	// per-doc check reads the step catalogue.
	stepDoc := func(name, reviewers string) docindex.Doc {
		body := "[meta]\nname = \"" + name + "\"\ntype = \"workflow\"\nscope = \"system\"\n" +
			"description = \"fixture\"\napplies_to = [\"*\"]\n\n" +
			"[coded]\nstatus = \"in_progress\"\nagent = \"executor\"\nrequires = [\"raised\"]\n"
		if reviewers != "" {
			body += "reviewers = [\"" + reviewers + "\"]\n"
		}
		return docindex.Doc{Name: name, Body: body}
	}
	a := stepDoc("wf-a", "does-not-exist")
	b := stepDoc("wf-b", "")
	resolve := func(string) bool { return false }

	single := WorkflowSkillProblems(a, resolve)
	if len(single) == 0 {
		t.Fatal("expected the unresolved skill to be reported per-doc")
	}
	for _, p := range single {
		if strings.Contains(p, "same precedence") {
			t.Errorf("ambiguity must not fire per-doc, got: %s", p)
		}
	}
	if !strings.Contains(strings.Join(single, "\n"), "does-not-exist") {
		t.Errorf("per-doc report must name the skill, got %v", single)
	}

	// Whole-set still reports BOTH: the unresolved skill and the ambiguity that
	// only exists across documents.
	whole := WorkflowConsistency([]docindex.Doc{a, b}, resolve)
	var sawAmbiguity, sawUnresolved bool
	for _, p := range whole {
		if strings.Contains(p, "same precedence") {
			sawAmbiguity = true
		}
		if strings.Contains(p, "does-not-exist") {
			sawUnresolved = true
		}
	}
	if !sawAmbiguity {
		t.Errorf("whole-set must still report ambiguity, got %v", whole)
	}
	if !sawUnresolved {
		t.Errorf("whole-set must still report the unresolved skill, got %v", whole)
	}
}

// TestWorkflowConsistencyMessageUnchanged (AC3): the extraction must be
// behaviour-preserving for the whole-set callers, which report these as FAILs —
// the exact string they have always printed.
func TestWorkflowConsistencyMessageUnchanged(t *testing.T) {
	d := docindex.Doc{Name: "wf", Body: "[meta]\nname = \"wf\"\ntype = \"workflow\"\nscope = \"system\"\n" +
		"description = \"fixture\"\napplies_to = [\"*\"]\n\n" +
		"[coded]\nstatus = \"in_progress\"\nagent = \"executor\"\n" +
		"reviewers = [\"missing-review\"]\nrequires = [\"raised\"]\n"}
	got := WorkflowConsistency([]docindex.Doc{d}, func(string) bool { return false })
	want := `workflow wf references skill "missing-review" which does not resolve in the substrate`
	var found bool
	for _, p := range got {
		if p == want {
			found = true
		}
	}
	if !found {
		t.Errorf("message text must be unchanged.\nwant: %s\ngot:  %v", want, got)
	}
}
