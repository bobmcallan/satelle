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

// TestEmbeddedReviewerVerdictStatesBlockingFindings: every judging reviewer is
// told what a blocking finding must carry and that a review with none accepts.
// Without that text each reviewer invents its own bar and rejects on
// hypotheticals, preferences or nits.
func TestEmbeddedReviewerVerdictStatesBlockingFindings(t *testing.T) {
	body := embeddedBody(t, "principles", "satelle-reviewer-verdict")
	for _, want := range []string{
		"## Blocking findings",
		"a hypothetical is not a finding",
		"sits on this reviewer's own axis",
		"A review with no such finding accepts",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("satelle-reviewer-verdict does not state %q", want)
		}
	}
}
