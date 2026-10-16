package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// sty_89804a0e: an accepted `story amend` of the acceptance criteria outranks
// the plan section that entry to an apply_criteria step would otherwise copy
// back. The fixture route has a non-terminal state after the apply step and a
// declared recover back into it, which is the rework path the defect came from.

const amendApplyDoneToml = `["*"]
obligations = ["raised", "planned", "coded", "released", "closed"]
recover = { step = "in_progress", from = ["release"] }
`

const amendApplyStepToml = `[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "planner"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
freeze = true
requires = ["planned"]
` + planCriteriaKnob + `
[released]
status = "release"
agent = "executor"
requires = ["coded"]

[closed]
status = "done"
terminal = true
requires = ["released"]
`

const (
	oldPlanCriteria = "1. old criterion"
	newPlanCriteria = "1. new criterion"
)

func planWith(criteria string) string {
	return "# Plan\n\n## Acceptance criteria\n\n" + criteria + "\n\n## Risks\n\nNone.\n"
}

func TestApplyPlannedCriteria_AcceptedAmendSurvivesReentry(t *testing.T) {
	db := wireWithWorkflowsStore(t, routeHalves(amendApplyDoneToml, amendApplyStepToml))
	verb.SetStoryDir(t.TempDir())
	verb.SetAmendReviewer(&amendStub{dec: verb.GateDecision{Gated: true, Accept: true, Skill: "amend-review"}})
	var out strings.Builder
	verb.SetApplyOutput(&out)

	it := newStoryWithCriteria(t, "")
	call(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	attachPlan(t, it.ID, planWith(oldPlanCriteria))
	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	if got := statusOf(t, it.ID).AcceptanceCriteria; got != oldPlanCriteria {
		t.Fatalf("first apply = %q, want the plan's criteria %q", got, oldPlanCriteria)
	}
	call(t, "story-set", map[string]any{"id": it.ID, "status": "release"})
	call(t, "story-amend", map[string]any{"id": it.ID, "acceptance_criteria": newPlanCriteria, "reason": "AC1 was wrong"})
	if out.Len() != 0 {
		t.Fatalf("the first apply must print nothing, got %q", out.String())
	}

	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})

	// AC1: the amended text survives re-entry to the apply_criteria step.
	if got := statusOf(t, it.ID); got.Status != "in_progress" || got.AcceptanceCriteria != newPlanCriteria {
		t.Fatalf("story = %q / %q, want in_progress keeping the amended %q", got.Status, got.AcceptanceCriteria, newPlanCriteria)
	}
	// AC2: the transition names the story, the document, the heading and the drift.
	for _, want := range []string{it.ID, `"plan"`, `"Acceptance criteria"`, "differ from the amended criteria"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("notice %q should contain %q", out.String(), want)
		}
	}
	// AC4: no accepted-plan row follows the amendment.
	entries, err := db.Ledger.ListByStory(context.Background(), it.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	amendedAt := -1
	for i, e := range entries {
		if e.Kind == ledger.KindDefinitionAmended {
			amendedAt = i
		}
	}
	if amendedAt < 0 {
		t.Fatal("no definition_amended row on the ledger")
	}
	for _, e := range entries[amendedAt+1:] {
		if e.Kind != ledger.KindDefinitionEdited {
			continue
		}
		var p struct {
			Actor string `json:"actor"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		if p.Actor == "accepted-plan" {
			t.Errorf("definition_edited by accepted-plan after the amendment: %+v", e)
		}
	}
}

// The check must not skip too much: with no amendment on the ledger, re-entry
// still applies a changed plan section, as before.
func TestApplyPlannedCriteria_WithoutAmendReentryStillApplies(t *testing.T) {
	wireWithWorkflowsStore(t, routeHalves(amendApplyDoneToml, amendApplyStepToml))
	verb.SetStoryDir(t.TempDir())
	var out strings.Builder
	verb.SetApplyOutput(&out)

	it := newStoryWithCriteria(t, "")
	call(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	attachPlan(t, it.ID, planWith(oldPlanCriteria))
	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	call(t, "story-set", map[string]any{"id": it.ID, "status": "release"})
	attachPlan(t, it.ID, planWith(newPlanCriteria))
	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})

	if got := statusOf(t, it.ID).AcceptanceCriteria; got != newPlanCriteria {
		t.Fatalf("criteria = %q, want the re-attached plan's %q", got, newPlanCriteria)
	}
	if out.Len() != 0 {
		t.Fatalf("no notice expected without an amend, got %q", out.String())
	}
}
