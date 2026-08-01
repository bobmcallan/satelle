//go:build integration

package tests

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// TestExecutorAugmentation_ShippedWorkflowsUnchanged (sty_8225d8a5 AC4): no
// shipped lifecycle declares augmentation nodes; PerformingStates and
// ExecutorPathToDoneSkills are stable with or without surface tags. Both shipped
// lifecycles are DERIVED ROUTES now — this repo's own (sty_9835070d) and the
// binary's default (sty_3795e7f6) — so each is checked per declared category
// rather than per file.
func TestExecutorAugmentation_ShippedWorkflowsUnchanged(t *testing.T) {
	check := func(label string, spec wfdot.Spec) {
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
	for _, category := range []string{"*", "epic-parent", "parent", "substrate", "execution", "task"} {
		check("this repo's route ("+category+")", repoRouteSpec(t, category, nil))
	}
	for _, category := range embeddedRouteCategories(t) {
		check("the shipped route ("+category+")", embeddedRouteSpec(t, category, nil))
	}
}
