package config

import (
	"strings"
	"testing"
)

// TestEmbeddedAgentGoalsTellsTheOrchestratorToPlanTheEpicFirst (sty_c931cd90):
// the shipped principle tells the orchestrator that, given an epic or a child of
// an epic whose readiness does not hold, it runs the readiness round first and
// unprompted, and that it owns each child's delivery. The text must sit under
// "Driving an epic", the section an orchestrator reads before engaging children.
func TestEmbeddedAgentGoalsTellsTheOrchestratorToPlanTheEpicFirst(t *testing.T) {
	body := embeddedBody(t, "principles", "satelle-agent-goals")
	_, section, found := strings.Cut(body, "## Driving an epic")
	if !found {
		t.Fatal("satelle-agent-goals has no 'Driving an epic' section")
	}
	// The section ends at the next heading.
	if i := strings.Index(section, "\n## "); i >= 0 {
		section = section[:i]
	}
	section = strings.Join(strings.Fields(section), " ")
	for _, want := range []string{
		"When you are given an epic, or a child of an epic whose readiness does not hold",
		"run `satelle story ready <epic>` first",
		"before engaging any child",
		"without waiting to be told",
		"You own each child's delivery",
		"`in-loop`, `local` for a subagent, or `team:<path>`",
		"`satelle story wave <epic>` prints it",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("satelle-agent-goals 'Driving an epic' does not state %q", want)
		}
	}
}

// ParsePin reads a child's placement pin in the placement vocabulary a binding
// uses, plus in-loop. remote is binding configuration, never a pin.
func TestParsePin(t *testing.T) {
	good := []struct{ in, kind, path string }{
		{"in-loop", PlacementInLoop, ""},
		{"local", PlacementLocal, ""},
		{"team:/teams/x", PlacementTeam, "/teams/x"},
		{"  Team:acme/ops ", PlacementTeam, "acme/ops"},
	}
	for _, tc := range good {
		kind, path, err := ParsePin(tc.in)
		if err != nil || kind != tc.kind || path != tc.path {
			t.Errorf("ParsePin(%q) = %q, %q, %v; want %q, %q", tc.in, kind, path, err, tc.kind, tc.path)
		}
	}
	for _, in := range []string{"remote", "", "team:", "bogus", "dispatched"} {
		if kind, _, err := ParsePin(in); err == nil {
			t.Errorf("ParsePin(%q) = %q, want an error", in, kind)
		}
	}
}

// Sharing the grammar with ParsePin must not change what a binding's placement
// resolves to.
func TestResolvedPlacementUnchangedBySharedGrammar(t *testing.T) {
	cases := []struct{ in, kind, path string }{
		{"", PlacementLocal, ""},
		{"local", PlacementLocal, ""},
		{"remote", PlacementRemote, ""},
		{"team:acme/ops", PlacementTeam, "acme/ops"},
		{"team:", "team:", ""},
		{"Bogus", "bogus", ""},
		{"in-loop", "in-loop", ""},
	}
	for _, tc := range cases {
		kind, path := AgentBinding{Placement: tc.in}.ResolvedPlacement()
		if kind != tc.kind || path != tc.path {
			t.Errorf("ResolvedPlacement(%q) = %q, %q; want %q, %q", tc.in, kind, path, tc.kind, tc.path)
		}
	}
}
