package wfgovern_test

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const clockDone = `[meta]
name = "done"
type = "workflow"
scope = "system"
description = "fixture declaration of done."

["*"]
obligations = ["raised", "planned", "parked", "closed"]
`

const clockStep = `[meta]
name = "step"
type = "workflow"
scope = "system"
description = "fixture step catalogue."

[raised]
status = "backlog"
start = true

[planned]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[parked]
status = "blocked"
agent = "reviewer"
requires = ["planned"]

[closed]
status = "done"
terminal = true
requires = ["planned"]
`

func clockDocs() []docindex.Doc {
	return []docindex.Doc{
		{Kind: "workflows", Name: "done", Body: clockDone, Embedded: true},
		{Kind: "workflows", Name: "step", Body: clockStep, Embedded: true},
	}
}

// TestClockForShape proves ClockFor carries no status literals of its own: it
// resolves engaging/terminal purely from the fixture's shape, and a park
// (blocked) target is engaging-false but also NOT terminal — a park leaves
// the clock running (sty_b8542a3a AC2).
func TestClockForShape(t *testing.T) {
	item := workitem.Item{Kind: workitem.KindStory, Status: "backlog"}
	clk, ok := wfgovern.ClockFor(clockDocs(), item)
	if !ok {
		t.Fatal("ClockFor: ok = false, want true")
	}
	if !clk.Engaging("in_progress") {
		t.Error(`Engaging("in_progress") = false, want true`)
	}
	if clk.Engaging("backlog") {
		t.Error(`Engaging("backlog") = true, want false (start state is not engaging)`)
	}
	if clk.Engaging("blocked") {
		t.Error(`Engaging("blocked") = true, want false (park is not an engaging state)`)
	}
	if clk.Terminal("blocked") {
		t.Error(`Terminal("blocked") = true, want false — a park must not stop the clock`)
	}
	if !clk.Terminal("done") {
		t.Error(`Terminal("done") = false, want true`)
	}
}

// TestClockForUnresolved proves an item whose workflow cannot be resolved
// degrades to ok=false rather than a guessed clock.
func TestClockForUnresolved(t *testing.T) {
	_, ok := wfgovern.ClockFor(nil, workitem.Item{Kind: workitem.KindStory, Status: "backlog"})
	if ok {
		t.Fatal("ClockFor with no workflows: ok = true, want false")
	}
}
