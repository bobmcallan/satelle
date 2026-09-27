package wfgovern

import (
	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// ClockFor resolves the workflow governing item through the one front door
// (SpecFor) and returns the costview.Clock built from its shape: Engaging is
// item's NonTerminalEngagingStates (the same rule the edit-gate and the
// single-story lease use), Terminal is IsTerminalState alone — never
// IsParkState, since a park/blocked transition leaves the story open. ok is
// false when no workflow resolves or its lifecycle does not parse; the caller
// then falls back to its own degraded clock rather than guessing at status
// literals.
func ClockFor(workflows []docindex.Doc, item workitem.Item) (clk costview.Clock, ok bool) {
	spec, _, _, err := SpecFor(workflows, item)
	if err != nil {
		return costview.Clock{}, false
	}
	engaging := map[string]bool{}
	for _, s := range spec.NonTerminalEngagingStates() {
		engaging[s] = true
	}
	return costview.Clock{
		Engaging: func(to string) bool { return engaging[to] },
		Terminal: func(to string) bool { return spec.IsTerminalState(to) },
	}, true
}
