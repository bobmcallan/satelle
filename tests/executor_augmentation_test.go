//go:build integration

package tests

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// checkNoAugmentation asserts a route declares no augmentation nodes,
// PerformingStates and ExecutorPathToDoneSkills are stable with or without
// surface tags, and the route validates.
func checkNoAugmentation(t *testing.T, label string, spec wfdot.Spec) {
	t.Helper()
	for _, st := range spec.States {
		if st.IsAugmentation() {
			t.Errorf("%s has unexpected augmentation %s", label, st.Name)
		}
	}
	a := spec.ExecutorPathToDoneSkills()
	b := spec.ExecutorPathToDoneSkillsFor([]string{"surface:ui", "surface:cli"})
	if len(a) != len(b) {
		t.Errorf("%s path skills nil-tags %v vs tagged %v", label, a, b)
	}
	if probs := wfdot.Validate(spec); len(probs) > 0 {
		t.Errorf("%s validate: %v", label, probs)
	}
}

// TestExecutorAugmentation_ShippedWorkflowsUnchanged (sty_8225d8a5 AC4): no
// shipped lifecycle declares augmentation nodes; PerformingStates and
// ExecutorPathToDoneSkills are stable with or without surface tags. The route
// the binary ships (sty_3795e7f6) is a DERIVED ROUTE, so it is checked per
// declared category rather than per file. This repo's own authored route is
// checked by TestOperatorRouteExecutorAugmentation (make operator-check).
func TestExecutorAugmentation_ShippedWorkflowsUnchanged(t *testing.T) {
	for _, category := range embeddedRouteCategories(t) {
		checkNoAugmentation(t, "the shipped route ("+category+")", embeddedRouteSpec(t, category, nil))
	}
}
