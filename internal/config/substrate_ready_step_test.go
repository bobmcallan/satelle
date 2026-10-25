package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// sty_25064a6c: the shipped container route carries the readiness round, so a repo
// with no .satelle/workflows of its own is sequenced before its children start.
// embeddedRouteSpecs resolves the shipped halves with no repo overlay, which is
// exactly the route such a repo runs.

// TestEmbeddedContainerRouteDeclaresReadiness proves each property of the shipped
// readiness step on both container categories.
func TestEmbeddedContainerRouteDeclaresReadiness(t *testing.T) {
	specs := embeddedRouteSpecs()
	for _, category := range []string{"epic-parent", "parent"} {
		spec, ok := specs[category]
		if !ok {
			t.Fatalf("%s: the shipped route derives nothing", category)
		}

		var ready wfdot.State
		var found bool
		for _, to := range spec.Successors(spec.Start()) {
			st, ok := spec.StateNamed(to)
			if ok && st.WaitsOnChildren {
				ready, found = st, true
				break
			}
		}
		if !found {
			t.Fatalf("%s: no step entered from the start declares waits_on_children", category)
		}

		if ready.Obligation != "ready" {
			t.Errorf("%s: readiness step discharges %q, want ready", category, ready.Obligation)
		}
		if ready.Mark != "ready" {
			t.Errorf("%s: mark = %q, want ready", category, ready.Mark)
		}
		if !ready.Propose {
			t.Errorf("%s: the readiness performer must run before the step's gates (propose)", category)
		}
		if got, want := ready.ApplyChangeOps, []string{"add-tags", "create-child"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: apply_changes ops = %v, want %v", category, got, want)
		}
		if got, want := ready.ApplyChangeTags, []string{"order:", "surface:", "lane:", "depends-on:", "placement:"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: apply_changes tags = %v, want %v", category, got, want)
		}
		// The orchestrator's per-child delivery choice (sty_c931cd90): the pin
		// prefix, the heading its reasons are recorded under, and the lane prefix
		// wave prints.
		if ready.PlacementTag != "placement:" || ready.DeliveryHeading != "Delivery" || ready.LaneTag != "lane:" {
			t.Errorf("%s: placement_tag/delivery_heading/lane_tag = %q/%q/%q, want placement:/Delivery/lane:",
				category, ready.PlacementTag, ready.DeliveryHeading, ready.LaneTag)
		}
		if ready.Agent != "ready-reviewer" {
			t.Errorf("%s: performer = %q, want ready-reviewer", category, ready.Agent)
		}
		if ready.Skill != "ready" {
			t.Errorf("%s: skill = %q, want ready", category, ready.Skill)
		}
	}

	// The rubric the step names ships in the embed tree, as an executor skill.
	var rubric string
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == "ready" {
			rubric = d.Body
		}
	}
	if rubric == "" {
		t.Fatal("the shipped readiness step names skill ready, but no such skill ships")
	}
	if !strings.Contains(rubric, "type:executor") {
		t.Error("skill ready must be tagged type:executor")
	}
	if !shippedExecutorSkills["ready"] {
		t.Error("skill ready must be in the closed shippedExecutorSkills list")
	}
}

// TestEmbeddedReadySeatIsReadOnly proves the seat that performs the shipped
// readiness step is the baseline ready-reviewer, that a repo with no agents file
// and no profile still resolves it a grant, and that the grant is read-only and
// carries the pull-context channel a dispatched agent needs.
func TestEmbeddedReadySeatIsReadOnly(t *testing.T) {
	base, err := BaselineAgents()
	if err != nil {
		t.Fatalf("BaselineAgents: %v", err)
	}
	if _, ok := base.NamedBinding("ready-reviewer"); !ok {
		t.Fatal("the baseline agents declare no ready-reviewer seat")
	}
	eff, prov, err := ResolveAgentsBaseline(base, AgentLayers{}, GlobalAgentsConfig{})
	if err != nil {
		t.Fatalf("ResolveAgentsBaseline: %v", err)
	}
	seat, ok := eff.NamedBinding("ready-reviewer")
	if !ok {
		t.Fatal("the resolved agents declare no ready-reviewer seat")
	}
	if strings.TrimSpace(seat.Tools) == "" {
		t.Fatal("an unconfigured repo must still resolve the seat a grant: init would otherwise fail validation")
	}
	for _, tool := range strings.Split(seat.Tools, ",") {
		switch strings.TrimSpace(tool) {
		case "Write", "Edit", "NotebookEdit", "Bash":
			t.Errorf("ready-reviewer grants %s; the seat is read-only", tool)
		}
	}
	if !BindingGrantsContextChannel(seat) {
		t.Errorf("grant %q carries no pull-context channel", seat.Tools)
	}
	if got := prov.Source("ready-reviewer", "tools"); got != SourceEmbedded {
		t.Errorf("tools source = %q, want %q", got, SourceEmbedded)
	}

	// A grant the repo writes wins over the embedded one.
	own := AgentLayers{Repo: AgentsConfig{Agents: map[string]AgentBinding{"ready-reviewer": {Tools: "Read"}}}}
	eff, _, err = ResolveAgentsBaseline(base, own, GlobalAgentsConfig{})
	if err != nil {
		t.Fatalf("ResolveAgentsBaseline with a repo grant: %v", err)
	}
	if seat, _ := eff.NamedBinding("ready-reviewer"); seat.Tools != "Read" {
		t.Errorf("a repo's own grant must win, got %q", seat.Tools)
	}
}
