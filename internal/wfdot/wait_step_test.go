package wfdot

import (
	"slices"
	"strings"
	"testing"
)

// A route wait step: `wait = true` parses onto the Step and the assembled State,
// classifies as neither park nor terminal nor engaging but at rest, and its
// mis-authorings are refused by name. The step and status names are fixture data.

const waitDone = `["*"]
obligations = ["raised", "coded", "queued", "closed"]
park = { state = "blocked", gate = "park-gate" }
cancel = { state = "cancelled", gate = "cancel-gate" }
`

func waitSteps(queuedExtra string) string {
	return `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[queued]
status = "awaiting_release"
wait = true
requires = ["coded"]
` + queuedExtra + `
[closed]
status = "done"
terminal = true
requires = ["queued"]
`
}

func TestWaitStepParsesAndClassifies(t *testing.T) {
	body := waitSteps("")
	cat, err := ParseSteps(body)
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	if !stepProviding(t, cat, "queued").Wait {
		t.Fatal("wait = true must reach Step.Wait")
	}
	if stepProviding(t, cat, "coded").Wait {
		t.Fatal("a step without the key does not wait")
	}
	spec, err := ParseRoute(waitDone, body, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	const w = "awaiting_release"
	switch {
	case !spec.IsWaitState(w):
		t.Error("IsWaitState must hold at the wait step")
	case spec.IsParkState(w):
		t.Error("a wait step is not a park")
	case spec.IsTerminalState(w):
		t.Error("a wait step is not terminal")
	case spec.IsFinal(w):
		t.Error("a wait step does not end the story")
	case !spec.IsAtRest(w):
		t.Error("a wait step is at rest")
	case slices.Contains(spec.NonTerminalEngagingStates(), w):
		t.Errorf("a wait step holds no engagement: %v", spec.NonTerminalEngagingStates())
	}
	if spec.IsWaitState("in_progress") || spec.IsAtRest("in_progress") {
		t.Error("a performing step neither waits nor rests")
	}
	if !spec.IsAtRest("done") || !spec.IsAtRest("blocked") || !spec.IsAtRest("cancelled") {
		t.Error("terminal and park states are at rest")
	}
	if spec.IsWaitState("nowhere") {
		t.Error("an unknown status does not wait")
	}
	if !spec.HasEdge("in_progress", w) || !spec.HasEdge(w, "done") {
		t.Errorf("the wait step sits on the spine: successors of %s = %v", w, spec.Successors(w))
	}
}

func TestWaitStepRefusesContradictions(t *testing.T) {
	cases := map[string]struct{ extra, want string }{
		"start":             {"start = true\n", "wait and start"},
		"terminal":          {"terminal = true\n", "wait and terminal"},
		"waits_on_children": {"waits_on_children = true\n", "wait and waits_on_children"},
		"remote_wait":       {`remote_wait = { remote = "r", on = { accept = "done" } }` + "\n", "wait and remote_wait"},
		"reviewer agent":    {"agent = \"reviewer\"\n", `wait and agent = "reviewer"`},
		"performer agent":   {"agent = \"executor\"\n", `wait and agent = "executor"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSteps(waitSteps(c.extra))
			if err == nil {
				t.Fatal("a contradictory wait step must be refused")
			}
			if !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), `"queued"`) {
				t.Errorf("error should name the step and %q: %v", c.want, err)
			}
		})
	}
}

// A wait step whose only exits are park and cancel could never be left onward.
func TestWaitStepRefusesNoOnwardEdge(t *testing.T) {
	done := `["*"]
obligations = ["raised", "coded", "queued"]
park = { state = "blocked", gate = "park-gate" }
cancel = { state = "cancelled", gate = "cancel-gate" }
`
	_, err := ParseRoute(done, waitSteps(""), "feature", nil)
	if err == nil {
		t.Fatal("a wait step with no onward edge must be refused")
	}
	if !strings.Contains(err.Error(), `"awaiting_release"`) || !strings.Contains(err.Error(), "no edge onward") {
		t.Errorf("error should name the step and the missing edge: %v", err)
	}
}
