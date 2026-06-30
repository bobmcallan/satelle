package web

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestTimelineDotOutcomeClass asserts the timeline dots are coloured by event
// outcome (sty_f19d2ec4): a review_reject <li> carries the fail class, a
// review_accept <li> the pass class, and neutral process events carry no outcome
// class (keeping the default accent dot). The class is rendered server-side, so the
// same itemDetail template covers BOTH the inline expansion and the standalone
// detail page.
func TestTimelineDotOutcomeClass(t *testing.T) {
	now := time.Now()
	d := detailData{
		Item: workitem.Item{ID: "sty_x", Kind: workitem.KindStory, Title: "x", Status: "in_progress", CreatedAt: now, UpdatedAt: now},
		Events: []ledger.Entry{
			{Kind: ledger.KindStoryCreated, Body: "created", CreatedAt: now},
			{Kind: ledger.KindStatusTransition, Body: "backlog → in_progress", CreatedAt: now},
			{Kind: ledger.KindReviewReject, Body: "rejected x→y", CreatedAt: now},
			{Kind: ledger.KindReviewAccept, Body: "accepted x→y", CreatedAt: now},
			{Kind: ledger.KindStepSummary, Body: "summary", CreatedAt: now},
		},
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "itemDetail", d); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if !strings.Contains(out, `<li class="tl-fail">`) {
		t.Errorf("a review_reject event should carry the fail dot class (tl-fail); got:\n%s", out)
	}
	if !strings.Contains(out, `<li class="tl-pass">`) {
		t.Errorf("a review_accept event should carry the pass dot class (tl-pass); got:\n%s", out)
	}
	// Neutral process events stay un-classed (default accent dot) — they render as a
	// bare <li>, never tl-pass/tl-fail.
	if strings.Count(out, `<li class="tl-`) != 2 {
		t.Errorf("only the two outcome-bearing events should be coloured; got %d classed dots", strings.Count(out, `<li class="tl-`))
	}
	if !strings.Contains(out, "<li>") {
		t.Errorf("neutral events should render as a bare <li> (default accent dot); got:\n%s", out)
	}
}

// TestTimelineDotPaletteReused asserts the dot colours reuse the existing
// review-light palette (no new ad-hoc colour values): tl-pass uses the pass green
// and tl-fail the fail red already defined for .review-light-*.
func TestTimelineDotPaletteReused(t *testing.T) {
	raw, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	for _, want := range []string{
		"ol.timeline li.tl-pass::before { background: #2ecc71; }",
		"ol.timeline li.tl-fail::before { background: #e74c3c; }",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css missing timeline dot rule reusing the review-light palette: %q", want)
		}
	}
	// The same hues are the review-light pass/fail values (single palette).
	for _, want := range []string{".review-light-pass { background: #2ecc71;", ".review-light-fail { background: #e74c3c;"} {
		if !strings.Contains(css, want) {
			t.Errorf("review-light palette anchor missing (%q) — timeline dots must reuse it", want)
		}
	}
}
