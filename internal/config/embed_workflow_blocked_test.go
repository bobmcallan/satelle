package config_test

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

// embeddedRoute derives the shipped default route for a category. The embedded
// lifecycle is done.md + step.md now, not a DOT graph (sty_3795e7f6).
func embeddedRoute(t *testing.T, category string) wfdot.Spec {
	t.Helper()
	var done, step string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind != "workflows" {
			continue
		}
		switch d.Name {
		case "done":
			done = d.Body
		case "step":
			step = d.Body
		}
	}
	if done == "" || step == "" {
		t.Fatal("the binary must ship both halves of the default route")
	}
	spec, err := wfdot.ParseRoute(done, step, category, nil)
	if err != nil {
		t.Fatalf("derive embedded route for %q: %v", category, err)
	}
	return spec
}

// TestEmbeddedBaselineOffersParkState pins AC1 of the blocked-lifecycle work:
// the shipped default lifecycle offers a park state (agent=reviewer, the
// blocked-review skill) reachable from a performing state and resumable back to
// one, and that park state is not itself performing — without hardcoding a
// product lifecycle name in Go mechanism. The park state is DECLARED on the
// wildcard section's `park:` line and the topology is synthesised, so this now
// asserts against the derived route rather than a graph.
func TestEmbeddedBaselineOffersParkState(t *testing.T) {
	spec := embeddedRoute(t, "*")

	// The park skill must appear on a node and on the edge into that node.
	const parkSkill = "satelle-story-blocked-review"
	var parkNode string
	for _, st := range spec.States {
		if st.Skill == parkSkill {
			parkNode = st.Name
			if st.Agent != "reviewer" {
				t.Errorf("park node %q agent = %q, want reviewer", st.Name, st.Agent)
			}
			if st.IsPerforming() {
				t.Errorf("park node %q must not be performing", st.Name)
			}
		}
	}
	if parkNode == "" {
		t.Fatal("the shipped route has no state gated by satelle-story-blocked-review")
	}
	if !spec.IsParkState(parkNode) {
		t.Errorf("state %q is declared as the park state but does not read as one", parkNode)
	}
	// Engagement: park must not be engaging (edit/commit gates).
	for _, s := range spec.NonTerminalEngagingStates() {
		if s == parkNode {
			t.Errorf("park node %q is engaging — must not be", parkNode)
		}
	}
	// Edges: some performing state reaches park, and park reaches on (resume /
	// cancel), so a parked story is never stranded.
	var intoPark, outOfPark bool
	for _, tr := range spec.Transitions {
		if tr.To == parkNode {
			intoPark = true
			if tr.Skill != parkSkill {
				t.Errorf("edge into park skill = %q, want %s", tr.Skill, parkSkill)
			}
		}
		if tr.From == parkNode && tr.To != parkNode {
			outOfPark = true
		}
	}
	if !intoPark || !outOfPark {
		t.Errorf("park edges missing: into=%v out=%v", intoPark, outOfPark)
	}
	// Skill file is embedded too.
	var skillOK bool
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == parkSkill {
			skillOK = true
			if !strings.Contains(d.Body, "reason") {
				t.Error("blocked-review skill body should require a reason")
			}
		}
	}
	if !skillOK {
		t.Fatalf("embedded skill %s missing", parkSkill)
	}
}
