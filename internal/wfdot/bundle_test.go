package wfdot

import "testing"

// sty_23e10d92: a step's `bundle` key declares that its entry gates which share a
// binding, model, effort and tool grant run as one reviewer session. Absent or
// false is today's default — every gate its own session.

func bundleSteps(bundleLine string) string {
	return `[raised]
status = "backlog"
start = true

[readied]
status = "plan"
agent = "planner"
skills = ["plan"]
reviewers = ["gate-a", "gate-b"]
` + bundleLine + `
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["readied"]
`
}

const bundleDone = `["*"]
obligations = ["raised", "readied", "closed"]
`

func planEdgeBundle(t *testing.T, bundleLine string) bool {
	t.Helper()
	spec, err := ParseRoute(bundleDone, bundleSteps(bundleLine), "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	for _, tr := range spec.Transitions {
		if tr.From == "backlog" && tr.To == "plan" {
			return tr.Bundle
		}
	}
	t.Fatal("no backlog→plan edge")
	return false
}

func TestBundleKeyParsesOntoTheEntryEdge(t *testing.T) {
	if !planEdgeBundle(t, "bundle = true") {
		t.Error("bundle = true must reach the edge")
	}
	if planEdgeBundle(t, "bundle = false") {
		t.Error("bundle = false must leave the edge unbundled")
	}
	if planEdgeBundle(t, "") {
		t.Error("an absent bundle key must leave the edge unbundled — the default is separate sessions")
	}
}

func TestBundleNeedsMoreThanOneGate(t *testing.T) {
	// One reviewer has nothing to share a session with; the edge never claims it.
	steps := `[raised]
status = "backlog"
start = true

[readied]
status = "plan"
skills = ["plan"]
agent = "planner"
reviewers = ["gate-a"]
bundle = true
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["readied"]
`
	spec, err := ParseRoute(bundleDone, steps, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	for _, tr := range spec.Transitions {
		if tr.To == "plan" && tr.Bundle {
			t.Errorf("a single-gate edge must not be bundled: %+v", tr)
		}
	}
}
