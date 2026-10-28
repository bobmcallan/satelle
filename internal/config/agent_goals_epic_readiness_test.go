package config

import (
	"strings"
	"testing"
)

// TestAgentGoalsStatesEpicReadiness (sty_87403c0e): the Driving an epic section
// gives the one account of what readying an epic does to its backlog children —
// no status change, the round runs each child's readiness round when
// ready_children is absent or true, and each child is planned when the wave lets
// it start only under ready_children = false.
func TestAgentGoalsStatesEpicReadiness(t *testing.T) {
	body, ok := embeddedPrinciples()["satelle-agent-goals"]
	if !ok {
		t.Fatal("satelle-agent-goals must be embedded")
	}
	flat := strings.Join(strings.Fields(body), " ")
	for _, want := range []string{
		"epic's own judgement of order, parallelism and achievability",
		"changes no child's status",
		"With `ready_children` absent or true it also runs each backlog child's readiness round (each stays in backlog)",
		"with `ready_children = false` each child is planned through its own backlog → plan step when `satelle story wave` lets it start",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("satelle-agent-goals Driving an epic must say %q", want)
		}
	}
}
