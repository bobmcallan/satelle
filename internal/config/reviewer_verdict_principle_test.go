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

// TestEmbeddedReviewerVerdictStatesCriteria (sty_99d3162b): a reviewer is told by
// this principle how a per-criterion verdict works — the criteria[] entry shape,
// that unchecked or omitted never accepts, that an objection outside any
// criterion is criterion 0, that only open criteria are judged when
// criteria_state is present, and how peer_findings are used. Without that text a
// reviewer has no way to know the gate decides from its criteria, and the skills
// would each restate it.
func TestEmbeddedReviewerVerdictStatesCriteria(t *testing.T) {
	body := embeddedBody(t, "principles", "satelle-reviewer-verdict")
	for _, want := range []string{
		"## Criteria",
		`"status": "accept"|"reject"|"unchecked"`,
		"An unchecked or omitted criterion never accepts",
		"whatever the top-level `decision` says",
		"An objection that belongs to no single criterion is criterion `0`",
		"judge only the criteria marked `open`",
		"`criteria_state`",
		"`peer_findings`",
		"never your own",
		"A verdict with no `criteria` is decided by its top-level `decision`",
		// The exception: with every criterion settled the carried accepts decide, so
		// a prose-only reject is dropped and a general objection must be criterion 0.
		"when every criterion is `settled` the binary carries those accepts and decides from them",
		"a prose-only reject is dropped",
		"itemise any objection that belongs to no single criterion as criterion `0` in `criteria`",
		"The whole-material judgment applies to a verdict without `criteria`",
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

// TestEmbeddedReviewerVerdictStatesPartialPriorVerdicts (sty_93359e0f): the
// re-presented-edge rule every judging seat receives says a partial prior
// verdict (a fail-fast reject, or any verdict whose coverage is not complete)
// leaves the remainder unjudged — judge it, never only the difference — and is
// never carried or re-issued as an accept. Without this text "judge the
// difference" reads a fail-fast reject as acceptance of what it never reviewed.
func TestEmbeddedReviewerVerdictStatesPartialPriorVerdicts(t *testing.T) {
	body := embeddedBody(t, "principles", "satelle-reviewer-verdict")
	for _, want := range []string{
		"review_mode",
		"fail-fast",
		"coverage",
		"stopped_at",
		"partial prior verdict",
		"`partial: true`",
		"the whole remainder of the material as a first review would",
		"not only the difference",
		"Never carry or re-issue a partial verdict as an accept",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("satelle-reviewer-verdict does not state %q", want)
		}
	}
}
