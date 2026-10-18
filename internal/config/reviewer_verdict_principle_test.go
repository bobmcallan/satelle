package config

import (
	"strings"
	"testing"
)

// TestEmbeddedReviewerVerdictStatesCarriedAccepts: a reviewer is told by this
// principle that an accept whose quotation is unchanged may be recorded as
// carried without dispatching it, and that the advancing round dispatches it
// anyway. Without that text a carried row reads as a verdict the reviewer never
// gave and the confirming pass as an unexplained second dispatch.
func TestEmbeddedReviewerVerdictStatesCarriedAccepts(t *testing.T) {
	body := embeddedBody(t, "principles", "satelle-reviewer-verdict")
	for _, want := range []string{
		"reviewer_carry",
		"carried_from",
		"without dispatching you",
		"When every dispatched reviewer accepts, you are dispatched in the same call",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("satelle-reviewer-verdict does not state %q", want)
		}
	}
}
