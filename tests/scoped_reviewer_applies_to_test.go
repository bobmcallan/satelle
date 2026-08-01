//go:build integration

package tests

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// TestScopedReviewerAppliesTo_ShippedWorkflowsUnchanged (sty_c6d093c8 AC3):
// none of the four active workflows carries step-level applies_to today, so
// ScopedReviewers(status, nil) equals ScopedReviewers(status, anyTags) for every
// declared status. Unknown attrs must not appear either (AC9 audit).
func TestScopedReviewerAppliesTo_ShippedWorkflowsUnchanged(t *testing.T) {
	// Both shipped lifecycles are DERIVED ROUTES now — this repo's own
	// (sty_9835070d) and the binary's default (sty_3795e7f6) — so each is checked
	// per declared category rather than per file.
	tags := []string{"surface:ui", "surface:cli", "web", "feature"}
	specs := map[string]wfdot.Spec{}
	for _, category := range []string{"*", "epic-parent", "parent", "substrate", "execution", "task"} {
		specs["this repo's route ("+category+")"] = repoRouteSpec(t, category, nil)
	}
	for _, category := range embeddedRouteCategories(t) {
		specs["the shipped route ("+category+")"] = embeddedRouteSpec(t, category, nil)
	}
	check := func(p string, spec wfdot.Spec) {
		if probs := wfdot.Validate(spec); len(probs) > 0 {
			t.Errorf("%s Validate: %v", p, probs)
		}
		statuses := map[string]bool{"in_progress": true, "done": true, "release": true, "plan": true}
		for _, st := range spec.States {
			statuses[st.Name] = true
			// After sty_e4359efe the project lifecycle may declare a surface-scoped
			// design gate; nothing else should carry applies_to.
			if len(st.AppliesTo) > 0 && st.Skill != "satelle-design-review" {
				t.Errorf("%s node %s has unexpected applies_to=%v", p, st.Name, st.AppliesTo)
			}
		}
		for status := range statuses {
			a := spec.ScopedReviewers(status, nil)
			b := spec.ScopedReviewers(status, tags)
			if len(b) < len(a) {
				t.Errorf("%s status %q: tags removed a scoped reviewer (%d → %d)", p, status, len(a), len(b))
			}
		}
	}
	for name, spec := range specs {
		check(name, spec)
	}
}
