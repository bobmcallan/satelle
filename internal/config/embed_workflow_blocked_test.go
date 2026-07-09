package config_test

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

// TestEmbeddedBaselineOffersParkState pins AC1 of the blocked-lifecycle work:
// the embedded baseline declares a park node (agent=reviewer, blocked-review
// skill) with in_progress↔park edges, and that park node is not a performing
// state — without hardcoding a product lifecycle name in Go mechanism.
func TestEmbeddedBaselineOffersParkState(t *testing.T) {
	var body string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "workflows" && d.Name == "satelle-baseline-workflow" {
			body = d.Body
			break
		}
	}
	if body == "" {
		t.Fatal("embedded baseline workflow missing")
	}
	spec, ok := wfdot.Parse(body)
	if !ok {
		t.Fatal("baseline DOT parse failed")
	}

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
		t.Fatal("baseline has no node with @skill:satelle-story-blocked-review")
	}
	// Engagement: park must not be engaging (edit/commit gates).
	for _, s := range spec.NonTerminalEngagingStates() {
		if s == parkNode {
			t.Errorf("park node %q is engaging — must not be", parkNode)
		}
	}
	// Edges: some performing state reaches park, and park reaches a performing state (resume).
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
