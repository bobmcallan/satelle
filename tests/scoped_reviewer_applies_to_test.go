//go:build integration

package tests

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// checkScopedAppliesTo asserts a route carries no step-level applies_to beyond
// the surface-scoped design gate, and that tags never remove a scoped reviewer.
func checkScopedAppliesTo(t *testing.T, p string, spec wfdot.Spec) {
	t.Helper()
	tags := []string{"surface:ui", "surface:cli", "web", "feature"}
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

// TestScopedReviewerAppliesTo_ShippedWorkflowsUnchanged (sty_c6d093c8 AC3): the
// route the binary ships (sty_3795e7f6) carries no step-level applies_to, so
// ScopedReviewers(status, nil) equals ScopedReviewers(status, anyTags) for every
// declared status. Unknown attrs must not appear either (AC9 audit). It is a
// DERIVED ROUTE, so each declared category is checked rather than each file;
// this repo's own authored route is checked by
// TestOperatorRouteScopedReviewerAppliesTo (make operator-check).
func TestScopedReviewerAppliesTo_ShippedWorkflowsUnchanged(t *testing.T) {
	for _, category := range embeddedRouteCategories(t) {
		checkScopedAppliesTo(t, "the shipped route ("+category+")", embeddedRouteSpec(t, category, nil))
	}
}
