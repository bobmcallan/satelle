package wfgovern_test

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const waitsDone = `[meta]
name = "done"
type = "workflow"
scope = "system"
description = "fixture declaration of done."

["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }

[container]
obligations = ["raised", "ready", "wrapped"]
park = { state = "blocked" }
cancel = { state = "cancelled" }
`

const waitsStep = `[meta]
name = "step"
type = "workflow"
scope = "system"
description = "fixture step catalogue."

[raised]
status = "backlog"
start = true

[ready]
status = "ready"
agent = "ready-reviewer"
waits_on_children = true
requires = ["raised"]

[wrapped]
status = "wrapped"
agent = "executor"
requires = ["ready"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`

// TestWaitsOnOpenChildren (sty_56648ae5): a container waits only at a step its
// route declares, and only while a child is open under ChildResolved's rule.
func TestWaitsOnOpenChildren(t *testing.T) {
	docs := []docindex.Doc{
		{Kind: "workflows", Name: "done", Body: waitsDone, Embedded: true},
		{Kind: "workflows", Name: "step", Body: waitsStep, Embedded: true},
	}
	box := workitem.Item{ID: "sty_box", Kind: workitem.KindStory, Category: "container", Status: "ready"}
	child := func(status string) workitem.Item {
		return workitem.Item{ID: "sty_kid", Kind: workitem.KindStory, Category: "feature", ParentID: "sty_box", Status: status}
	}
	spec, _, _, err := wfgovern.SpecFor(docs, box)
	if err != nil {
		t.Fatalf("SpecFor: %v", err)
	}

	cases := []struct {
		name   string
		status string
		items  []workitem.Item
		want   bool
	}{
		{"open child", "ready", []workitem.Item{box, child("in_progress")}, true},
		{"all children terminal", "ready", []workitem.Item{box, child("done")}, false},
		{"parked-resuming child is still open", "ready", []workitem.Item{box, child("blocked")}, true},
		{"cancelled child is resolved", "ready", []workitem.Item{box, child("cancelled")}, false},
		{"no children", "ready", []workitem.Item{box}, false},
		{"non-waiting step", "wrapped", []workitem.Item{box, child("in_progress")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wfgovern.WaitsOnOpenChildren(box, tc.status, spec, tc.items, docs); got != tc.want {
				t.Fatalf("WaitsOnOpenChildren = %v, want %v", got, tc.want)
			}
		})
	}
}
