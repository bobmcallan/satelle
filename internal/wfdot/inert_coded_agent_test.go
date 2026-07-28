package wfdot

import (
	"strings"
	"testing"
)

func TestInertCodedCheckAgentFindings_NodeOnly(t *testing.T) {
	body := "---\nname: t\n---\n" + "```dot" + `
digraph t {
  graph [goal="g", vars="v"]
  rankdir=LR
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done [shape=Msquare]
  estimate [agent=reviewer-summary, prompt="@skill:satelle-estimate-actual-review", on="in_progress,done"]
  llmgate [agent=reviewer, prompt="@skill:satelle-story-done-review", on="done"]
  backlog -> in_progress [agent=reviewer, prompt="@skill:satelle-story-intent-review"]
  in_progress -> done
}
` + "```\n"
	isCoded := func(skill string) bool {
		return skill == "satelle-estimate-actual-review"
	}
	fs, ok := InertCodedCheckAgentFindings(body, isCoded)
	if !ok {
		t.Fatal("parse failed")
	}
	if len(fs) != 1 {
		t.Fatalf("want 1 finding (estimate only), got %d: %v", len(fs), fs)
	}
	if fs[0].Kind != InertCodedCheckAgentKind || fs[0].Where != "estimate" {
		t.Fatalf("unexpected finding: %+v", fs[0])
	}
	// LLM gate and intent edge must not fire.
	for _, f := range fs {
		if f.Where == "llmgate" || strings.Contains(f.Where, "->") {
			t.Errorf("false positive on %s", f.Where)
		}
	}
}

func TestInertCodedCheckAgentFindings_EdgeDefaultAgentExempt(t *testing.T) {
	body := "---\nname: t\n---\n" + "```dot" + `
digraph t {
  graph [goal="g", vars="v"]
  rankdir=LR
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done [shape=Msquare]
  backlog -> in_progress [agent=reviewer, prompt="@skill:satelle-task-validate-before-review"]
  in_progress -> done [agent=reviewer-summary, prompt="@skill:satelle-task-validate-before-review"]
}
` + "```\n"
	isCoded := func(skill string) bool {
		return skill == "satelle-task-validate-before-review"
	}
	fs, ok := InertCodedCheckAgentFindings(body, isCoded)
	if !ok {
		t.Fatal("parse failed")
	}
	// agent=reviewer edge: no finding (parse exception).
	// agent=reviewer-summary edge: finding.
	if len(fs) != 1 {
		t.Fatalf("want 1 finding (non-default edge only), got %d: %v", len(fs), fs)
	}
	if fs[0].Where != "in_progress->done" {
		t.Fatalf("where=%s, want in_progress->done", fs[0].Where)
	}
}

func TestInertCodedCheckAgentFindings_NilPredicateNoOp(t *testing.T) {
	body := "---\nname: t\n---\n" + "```dot" + `
digraph t {
  graph [goal="g", vars="v"]
  rankdir=LR
  backlog [shape=Mdiamond]
  estimate [agent=reviewer, prompt="@skill:satelle-estimate-actual-review", on="done"]
  done [shape=Msquare]
  backlog -> done
}
` + "```\n"
	fs, ok := InertCodedCheckAgentFindings(body, nil)
	if !ok || len(fs) != 0 {
		t.Fatalf("nil predicate must yield no findings, ok=%v fs=%v", ok, fs)
	}
}

func TestRefresh_StripsInertCodedAgent(t *testing.T) {
	body := "---\nname: t\n---\n" + "```dot" + `
digraph t {
  graph [goal="g", vars="v"]
  rankdir=LR
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done [shape=Msquare]
  estimate [agent=reviewer-summary, prompt="@skill:satelle-estimate-actual-review", on="in_progress,done"]
  design [agent=reviewer, prompt="@skill:satelle-design-review", on="done"]
  backlog -> in_progress [agent=reviewer, prompt="@skill:intent"]
  in_progress -> done
}
` + "```\n"
	isCoded := func(skill string) bool {
		return skill == "satelle-estimate-actual-review"
	}
	out, changed, report := Refresh(body, nil, isCoded)
	if !changed {
		t.Fatal("expected strip of inert agent=")
	}
	if strings.Contains(out, `estimate [agent=`) {
		t.Errorf("estimate still has agent=:\n%s", out)
	}
	if !strings.Contains(out, `estimate [prompt="@skill:satelle-estimate-actual-review"`) &&
		!strings.Contains(out, `estimate    [prompt="@skill:satelle-estimate-actual-review"`) {
		// after strip, prompt and on remain
		if !strings.Contains(out, `prompt="@skill:satelle-estimate-actual-review"`) {
			t.Errorf("estimate prompt lost:\n%s", out)
		}
	}
	// LLM design node keeps agent=reviewer
	if !strings.Contains(out, `design [agent=reviewer`) && !strings.Contains(out, `design    [agent=reviewer`) {
		if !strings.Contains(out, `agent=reviewer, prompt="@skill:satelle-design-review"`) {
			t.Errorf("LLM design gate agent= must remain:\n%s", out)
		}
	}
	found := false
	for _, f := range report.Applied {
		if f.Kind == InertCodedCheckAgentKind && f.Where == "estimate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Applied missing inert strip: %v", report.Applied)
	}
	// Dry-run semantics: original body unchanged when we don't write — caller
	// owns write. Refresh itself only returns newBody.
	if body == out {
		t.Error("body and out should differ")
	}
	// Idempotent after strip.
	out2, changed2, _ := Refresh(out, nil, isCoded)
	if changed2 {
		t.Errorf("second refresh must be no-op, got:\n%s", out2)
	}
}

func TestAgentLessScopedGateIsNotPerforming(t *testing.T) {
	// Behaviour-neutrality: agent-less scoped on= gate is not performing, not
	// an augmentation, absent from PerformingStates (wfdot.go:328-332).
	body := "---\nname: t\n---\n" + "```dot" + `
digraph t {
  graph [goal="g", vars="v"]
  rankdir=LR
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done [shape=Msquare]
  estimate [prompt="@skill:satelle-estimate-actual-review", on="in_progress,done"]
  estimate_with [agent=reviewer, prompt="@skill:satelle-estimate-actual-review", on="done"]
  backlog -> in_progress
  in_progress -> done
}
` + "```\n"
	spec, ok := Parse(body)
	if !ok {
		t.Fatal("parse")
	}
	var est, estWith State
	for _, st := range spec.States {
		switch st.Name {
		case "estimate":
			est = st
		case "estimate_with":
			estWith = st
		}
	}
	if est.Name == "" || estWith.Name == "" {
		t.Fatal("missing estimate nodes")
	}
	if est.Agent != "" {
		t.Fatalf("agent-less node Agent=%q", est.Agent)
	}
	if est.IsPerforming() || est.IsAugmentation() {
		t.Error("agent-less scoped gate must not be performing/augmentation")
	}
	if estWith.IsPerforming() || estWith.IsAugmentation() {
		t.Error("agent=reviewer scoped gate must not be performing/augmentation")
	}
	for _, name := range spec.PerformingStates() {
		if name == "estimate" || name == "estimate_with" {
			t.Errorf("PerformingStates must not include %s", name)
		}
	}
	// ScopedReviewers enqueues both.
	got := spec.ScopedReviewers("in_progress", nil)
	found := false
	for _, r := range got {
		if r.Skill == "satelle-estimate-actual-review" {
			found = true
			if r.Agent != "" {
				// agent-less node contributes empty Agent
			}
		}
	}
	if !found {
		t.Fatalf("ScopedReviewers(in_progress) missing estimate: %v", got)
	}
	// applies_to placement: agent-less on= node is not rejected by Validate
	// (checks key on IsPerforming and Agent==reviewer && len(On)==0).
	if p := Validate(spec); len(p) != 0 {
		t.Errorf("Validate problems: %v", p)
	}
}
