//go:build integration && operatorconfig

package tests

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// The checks here pin this repo's AUTHORED route under .satelle/workflows. That
// directory is gitignored operator substrate, so a clean checkout (CI) does not
// carry it; like the other operator-config pins they run only under the
// operatorconfig opt-in (make operator-check), never in the hermetic suites. The
// route the BINARY ships is covered, with no dependency on the checkout, by the
// integration tests these share their check functions with.

// operatorRouteCategories are the categories this repo's route declares.
var operatorRouteCategories = []string{"*", "epic-parent", "parent", "substrate", "execution", "task"}

// TestOperatorRouteExecutorAugmentation: this repo's route declares no
// augmentation nodes and validates, per declared category.
func TestOperatorRouteExecutorAugmentation(t *testing.T) {
	for _, category := range operatorRouteCategories {
		checkNoAugmentation(t, "this repo's route ("+category+")", repoRouteSpec(t, category, nil))
	}
}

// TestOperatorRouteScopedReviewerAppliesTo: this repo's route carries no
// step-level applies_to beyond the surface-scoped design gate, per declared
// category.
func TestOperatorRouteScopedReviewerAppliesTo(t *testing.T) {
	for _, category := range operatorRouteCategories {
		checkScopedAppliesTo(t, "this repo's route ("+category+")", repoRouteSpec(t, category, nil))
	}
}

// TestOperatorDesignGateSurfaceScoped (sty_e4359efe): project workflow declares
// design with applies_to surface:ui on integration; only UI-tagged stories
// enqueue it.
func TestOperatorDesignGateSurfaceScoped(t *testing.T) {
	spec := repoRouteSpec(t, "*", nil)
	if probs := wfdot.Validate(spec); len(probs) > 0 {
		t.Fatalf("validate: %v", probs)
	}
	// design gate present with applies_to. A derived route names a gate node
	// after its skill (gate_<skill>) — the node name carries no contract.
	found := false
	for _, st := range spec.States {
		if st.Skill == "satelle-design-review" {
			found = true
			if len(st.AppliesTo) != 1 || st.AppliesTo[0] != "surface:ui" {
				t.Errorf("applies_to = %v", st.AppliesTo)
			}
			if !containsStrSlice(st.On, "integration") {
				t.Errorf("on = %v", st.On)
			}
		}
	}
	if !found {
		t.Fatal("design node missing from project workflow route")
	}
	ui := skillNames(spec.ScopedReviewers("integration", []string{"surface:ui"}))
	cli := skillNames(spec.ScopedReviewers("integration", []string{"surface:cli"}))
	if !ui["satelle-design-review"] {
		t.Errorf("surface:ui must enqueue design-review: %v", ui)
	}
	if cli["satelle-design-review"] {
		t.Errorf("surface:cli must NOT enqueue design-review: %v", cli)
	}
	// Spine transitions identical regardless of tags (no fork)
	if len(spec.Transitions) < 5 {
		t.Errorf("unexpected sparse spine: %d edges", len(spec.Transitions))
	}
}

// TestOperatorParentRoute: this repo's container categories (epic-parent,
// parent) close backlog → ready → merging → release → done with no slice steps.
func TestOperatorParentRoute(t *testing.T) {
	for _, cat := range []string{"epic-parent", "parent"} {
		spec := repoRouteSpec(t, cat, nil)
		var names []string
		for _, st := range spec.States {
			names = append(names, st.Name)
		}
		for _, absent := range []string{"plan", "in_progress", "integration"} {
			if containsStrSlice(names, absent) {
				t.Errorf("category %s must have no %q step — a container has no slice of its own (states %v)", cat, absent, names)
			}
		}
		if !spec.HasEdge("backlog", "ready") || !spec.HasEdge("ready", "merging") || !spec.HasEdge("merging", "release") || !spec.HasEdge("release", "done") {
			t.Errorf("category %s must close backlog → ready → merging → release → done (states %v)", cat, names)
		}
	}
}
