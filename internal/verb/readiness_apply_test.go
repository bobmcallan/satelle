package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_4d9df9a0: entry to a step that declares apply_criteria applies the
// criteria authored in the named story document. The fixture route declares the
// knob; the behaviour under test is the binary reading it, not this repo's names.

const applyDoneToml = `["*"]
obligations = ["raised", "coded", "closed"]
`

func applyStepToml(knob string) string {
	return `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
freeze = true
requires = ["raised"]
` + knob + `

[closed]
status = "done"
terminal = true
requires = ["coded"]
`
}

const planCriteriaKnob = "apply_criteria = { doc = \"plan\", heading = \"Acceptance criteria\" }\n"

// criteriaGater records the item it was asked to judge, so a test can assert the
// reviewers see the applied criteria and not a stale snapshot.
type criteriaGater struct{ seen []workitem.Item }

func (g *criteriaGater) Gate(_ context.Context, it workitem.Item, _ string) (verb.GateDecision, error) {
	g.seen = append(g.seen, it)
	return verb.GateDecision{}, nil
}

func newApplyRig(t *testing.T, knob string) (*criteriaGater, string) {
	t.Helper()
	withWiring(t)
	wireWithWorkflows(t, routeHalves(applyDoneToml, applyStepToml(knob)))
	dir := filepath.Join(t.TempDir(), ".satelle", "stories")
	verb.SetStoryDir(dir)
	g := &criteriaGater{}
	verb.SetTransitionGater(g)
	return g, dir
}

func newStoryWithCriteria(t *testing.T, criteria string) workitem.Item {
	t.Helper()
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "T", "body": "B", "acceptance_criteria": criteria, "category": "feature",
	}), &it); err != nil {
		t.Fatal(err)
	}
	return it
}

func attachPlan(t *testing.T, id, body string) {
	t.Helper()
	call(t, "story-doc-attach", map[string]any{"story_id": id, "name": "plan", "type": "plan", "body": body})
}

func definitionEdits(t *testing.T, id string) []verb.DefinitionEdit {
	t.Helper()
	var res verb.DefinitionEditsResult
	if err := json.Unmarshal(call(t, "story-definition-edits", map[string]any{"id": id}), &res); err != nil {
		t.Fatal(err)
	}
	return res.Edits
}

const plannedCriteria = "1. First criterion,\n   continued on a second line.\n2. Second criterion."

const planWithCriteria = "# Plan\n\n## Premise\n\nIt holds.\n\n## Acceptance criteria\n\n" + plannedCriteria +
	"\n\n## Risks\n\nNone.\n"

// AC1: the criteria in the accepted plan land on the story verbatim, newlines
// kept, the reviewers' payload carries them, and the edit is recorded as applied
// from the plan.
func TestApplyPlannedCriteria_AppliesVerbatimAndRecords(t *testing.T) {
	g, _ := newApplyRig(t, planCriteriaKnob)
	it := newStoryWithCriteria(t, "")
	attachPlan(t, it.ID, planWithCriteria)

	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})

	if got := statusOf(t, it.ID); got.Status != "in_progress" || got.AcceptanceCriteria != plannedCriteria {
		t.Fatalf("story = %q / %q, want in_progress with the plan's criteria verbatim", got.Status, got.AcceptanceCriteria)
	}
	if len(g.seen) != 1 || g.seen[0].AcceptanceCriteria != plannedCriteria {
		t.Fatalf("the gater must judge the applied criteria, saw %+v", g.seen)
	}
	rows := ledgerRows(t, it.ID, ledger.KindDefinitionEdited)
	if len(rows) != 1 {
		t.Fatalf("definition_edited rows = %d, want 1", len(rows))
	}
	edits := definitionEdits(t, it.ID)
	if len(edits) != 1 {
		t.Fatalf("definition edits = %d, want 1", len(edits))
	}
	if e := edits[0]; e.Field != "acceptance_criteria" || e.Actor != "accepted-plan" || e.Source != "plan#Acceptance criteria" || e.New != plannedCriteria {
		t.Fatalf("edit = %+v, want acceptance_criteria applied from plan#Acceptance criteria by accepted-plan", e)
	}
}

// Criteria the story already carries are not rewritten and leave no row.
func TestApplyPlannedCriteria_AlreadyEqualWritesNothing(t *testing.T) {
	newApplyRig(t, planCriteriaKnob)
	it := newStoryWithCriteria(t, plannedCriteria)
	attachPlan(t, it.ID, planWithCriteria)

	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})

	if rows := ledgerRows(t, it.ID, ledger.KindDefinitionEdited); len(rows) != 0 {
		t.Fatalf("definition_edited rows = %d, want none for text already carried", len(rows))
	}
}

// AC2: a plan that authored no criteria leaves the story's criteria unchanged,
// and the reviewers still see the story's own.
func TestApplyPlannedCriteria_NoSectionLeavesCriteria(t *testing.T) {
	cases := map[string]struct {
		knob string
		plan *string // nil: no plan document attached
	}{
		"no acceptance section": {planCriteriaKnob, ptr("# Plan\n\n## Premise\n\nIt holds.\n")},
		"empty section":         {planCriteriaKnob, ptr("# Plan\n\n## Acceptance criteria\n\n## Risks\n\nNone.\n")},
		"no plan document":      {planCriteriaKnob, nil},
		"step declares no knob": {"", ptr(planWithCriteria)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g, _ := newApplyRig(t, tc.knob)
			it := newStoryWithCriteria(t, "1. original")
			if tc.plan != nil {
				attachPlan(t, it.ID, *tc.plan)
			}

			call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})

			if got := statusOf(t, it.ID).AcceptanceCriteria; got != "1. original" {
				t.Fatalf("criteria = %q, want them unchanged", got)
			}
			if len(g.seen) != 1 || g.seen[0].AcceptanceCriteria != "1. original" {
				t.Fatalf("the gater must judge the story's own criteria, saw %+v", g.seen)
			}
			if rows := ledgerRows(t, it.ID, ledger.KindDefinitionEdited); len(rows) != 0 {
				t.Fatalf("definition_edited rows = %d, want none", len(rows))
			}
		})
	}
}

// A plan document that cannot be read (here a directory where the file should
// be) refuses the transition rather than skipping the apply silently.
func TestApplyPlannedCriteria_UnreadablePlanRefuses(t *testing.T) {
	_, dir := newApplyRig(t, planCriteriaKnob)
	it := newStoryWithCriteria(t, "1. original")
	attachPlan(t, it.ID, planWithCriteria)
	planPath := filepath.Join(dir, it.ID, "plan.md")
	if err := os.Remove(planPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(planPath, 0o755); err != nil {
		t.Fatal(err)
	}

	err := dispatchErr(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	if !strings.Contains(err.Error(), "applying") && !strings.Contains(err.Error(), "reading") {
		t.Fatalf("want an apply error, got %v", err)
	}
	if got := statusOf(t, it.ID).Status; got != "backlog" {
		t.Fatalf("status = %q, want the story to stay in backlog", got)
	}
}

func ptr(s string) *string { return &s }
