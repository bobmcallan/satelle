package wfgovern_test

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const dischargedDone = `[meta]
name = "done"
type = "workflow"
scope = "system"
description = "fixture declaration of done."

["*"]
obligations = ["raised", "planned", "coded", "integrated", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }

[chore]
obligations = ["raised", "chore-done"]
cancel = { state = "cancelled" }
`

const dischargedStep = `[meta]
name = "step"
type = "workflow"
scope = "system"
description = "fixture step catalogue."

[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "executor"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["planned"]

[integrated]
status = "integration"
agent = "executor"
requires = ["coded"]

[closed]
status = "done"
terminal = true
requires = ["integrated"]

[chore-done]
status = "done"
terminal = true
requires = ["raised"]
`

// TestChildDischarged (sty_fdad98b0): a member has discharged an obligation when
// it is resolved or at/past the route step providing it, by route order — and a
// member whose route has no such step, or whose route does not resolve, has not.
func TestChildDischarged(t *testing.T) {
	docs := []docindex.Doc{
		{Kind: "workflows", Name: "done", Body: dischargedDone, Embedded: true},
		{Kind: "workflows", Name: "step", Body: dischargedStep, Embedded: true},
	}
	item := func(category, status string) workitem.Item {
		return workitem.Item{ID: "sty_kid", Kind: workitem.KindStory, Category: category, Status: status}
	}
	cases := []struct {
		name                  string
		category, status      string
		wantDischarged, known bool
	}{
		{"before the step", "feature", "plan", false, true},
		{"entry state", "feature", "backlog", false, true},
		{"at the step", "feature", "in_progress", true, true},
		{"past the step", "feature", "integration", true, true},
		{"terminal", "feature", "done", true, true},
		{"cancelled never holds the container", "feature", "cancelled", true, true},
		{"a resuming park has not discharged", "feature", "blocked", false, true},
		{"route lacks the obligation", "chore", "backlog", false, false},
		{"route lacking it but resolved", "chore", "done", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, known := wfgovern.ChildDischarged(docs, item(tc.category, tc.status), "coded")
			if got != tc.wantDischarged || known != tc.known {
				t.Errorf("ChildDischarged(%s/%s) = (%v,%v), want (%v,%v)", tc.category, tc.status, got, known, tc.wantDischarged, tc.known)
			}
		})
	}
}
