package agentstep

import (
	"context"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfgovern"
)

// The stamp round trip (sty_81bb0dde): whatever WorkflowNameFor hands out at
// create, WorkflowStates must accept back at restamp. They are the two halves of
// one contract, and nothing else pins them together.
//
// The derived route is the case that broke it. It is the one lifecycle with no
// document of its own — it is BUILT from the two route-source halves — so the
// doc lookup WorkflowStates used always missed, and `satelle story restamp`
// refused the very name the create path assigns: "workflow \"default\" does not
// resolve in the substrate". Naming the route made the failure visible; it was
// there under the old file-pair name too.
func TestDerivedRouteNameRoundTripsFromCreateToRestamp(t *testing.T) {
	docs := fakeDocs{workflow: plainWF}
	g, _ := newEngine(t, "", docs)

	name := g.WorkflowNameFor(context.Background(), "feature")
	if name != wfgovern.DerivedRouteName {
		t.Fatalf("WorkflowNameFor = %q, want the derived route %q", name, wfgovern.DerivedRouteName)
	}
	states, resolved := g.WorkflowStates(context.Background(), name)
	if !resolved {
		t.Fatalf("WorkflowStates(%q) must resolve — restamp refuses a name that does not", name)
	}
	// A route's states depend on the story's CATEGORY, which a name alone does not
	// carry, so the contract is "resolves, declares nothing" and the caller skips
	// the status check rather than stranding the story.
	if len(states) != 0 {
		t.Errorf("a derived route declares no states by name alone, got %v", states)
	}
}

// The one case the name must NOT resolve: a repo with no route source at all.
// Resolving there would let a story be stamped onto a lifecycle that does not
// exist, which is the failure the check exists to prevent.
func TestDerivedRouteNameDoesNotResolveWithoutARouteSource(t *testing.T) {
	// A workflows doc that is not a route half — no done/step pair.
	docs := fakeDocs{extraWorkflows: []docindex.Doc{
		{Kind: "workflows", Name: "some-graph", Body: "---\nname: some-graph\n---\n# not a route\n"},
	}}
	g, _ := newEngine(t, "", docs)
	if _, resolved := g.WorkflowStates(context.Background(), wfgovern.DerivedRouteName); resolved {
		t.Fatal("the derived route must not resolve in a repo that has no route source")
	}
}

// Half a route is not a route: one half present must not resolve either, or a
// stamp would name a lifecycle that cannot be built.
func TestDerivedRouteNameDoesNotResolveOnHalfARoute(t *testing.T) {
	docs := fakeDocs{extraWorkflows: []docindex.Doc{
		{Kind: "workflows", Name: wfgovern.RouteSourceDone,
			Body: "[meta]\nname = \"done\"\n\n[\"*\"]\nobligations = [\"raised\"]\n"},
	}}
	g, _ := newEngine(t, "", docs)
	if _, resolved := g.WorkflowStates(context.Background(), wfgovern.DerivedRouteName); resolved {
		t.Fatal("one half is not a route — the name must not resolve")
	}
}
