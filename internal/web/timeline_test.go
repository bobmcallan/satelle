package web

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestSettingsTimelineFieldsControl asserts the project settings page hosts the
// viewer-local Timeline-fields checklist (sty_43d228e4) — the approved placement.
func TestSettingsTimelineFieldsControl(t *testing.T) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings", settingsData{RepoRoot: "/repo", TopBar: newTopBar("")}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, f := range []string{"walltime", "tokens", "model", "outcome"} {
		if !strings.Contains(out, `data-tlfield="`+f+`"`) {
			t.Errorf("settings page missing the Timeline fields checkbox for %q:\n%s", f, out)
		}
	}
}

// evs wraps ledger entries as the timeline view-models the detail template now
// takes, computing each row's data-driven telemetry chips (sty_43d228e4).
func evs(entries ...ledger.Entry) []eventVM {
	out := make([]eventVM, len(entries))
	for i, e := range entries {
		out[i] = eventVM{Entry: e, Chips: eventChips(e)}
	}
	return out
}

// TestTimelineChips asserts the timeline renders data-driven agent-action chips
// (sty_43d228e4 / sty_56aae77a): an agent_invocation row shows wall-time/tokens/model
// chips (no outcome) when usage is measured; an unreported invocation never shows a
// token chip; a telemetry_event shows its outcome; a plain status_transition shows none.
func TestTimelineChips(t *testing.T) {
	now := time.Now()
	// Legacy non-zero total still measures (no usage_available field).
	inv, _ := json.Marshal(map[string]any{"agent": "reviewer", "model": "sonnet", "tokens_total": 2000, "duration_ms": 2400})
	// Explicit unreported: must not show a tokens chip (sty_56aae77a).
	unrep, _ := json.Marshal(map[string]any{"agent": "reviewer", "model": "grok-4.5", "tokens_total": 0, "usage_available": false, "duration_ms": 800})
	tel, _ := json.Marshal(map[string]any{"kind": "agent-retry", "data": map[string]any{"outcome": "error"}})
	d := detailData{
		Item: workitem.Item{ID: "sty_x", Kind: workitem.KindStory, Title: "x", Status: "in_progress", CreatedAt: now, UpdatedAt: now},
		Events: evs(
			ledger.Entry{Kind: ledger.KindAgentInvocation, Body: "invoked", Payload: inv, CreatedAt: now},
			ledger.Entry{Kind: ledger.KindAgentInvocation, Body: "unreported", Payload: unrep, CreatedAt: now},
			ledger.Entry{Kind: ledger.KindTelemetryEvent, Body: "telemetry", Payload: tel, CreatedAt: now},
			ledger.Entry{Kind: ledger.KindStatusTransition, Body: "backlog → plan", CreatedAt: now},
		),
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "itemDetail", d); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`<span class="chip chip-walltime">2.4s</span>`,
		`<span class="chip chip-tokens">2,000 tok</span>`,
		`<span class="chip chip-model">sonnet</span>`,
		`<span class="chip chip-outcome">error</span>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("timeline missing chip %q; got:\n%s", want, out)
		}
	}
	// Unreported row: walltime + model only — never a tokens chip (measured zero
	// would still need UsageAvailable; unreported must not invent one).
	if strings.Contains(out, `chip-tokens">0`) || strings.Count(out, `chip-tokens`) != 1 {
		t.Errorf("want exactly one tokens chip (the measured 2,000); got:\n%s", out)
	}
	// Measured inv: walltime + tokens + model; unreported: walltime + model; tel: outcome = 6.
	if strings.Count(out, `class="chip `) != 6 {
		t.Errorf("want 6 chips (3 measured + 2 unreported + 1 outcome); got %d", strings.Count(out, `class="chip `))
	}
}

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
		Events: evs(
			ledger.Entry{Kind: ledger.KindStoryCreated, Body: "created", CreatedAt: now},
			ledger.Entry{Kind: ledger.KindStatusTransition, Body: "backlog → in_progress", CreatedAt: now},
			ledger.Entry{Kind: ledger.KindReviewReject, Body: "rejected x→y", CreatedAt: now},
			ledger.Entry{Kind: ledger.KindReviewAccept, Body: "accepted x→y", CreatedAt: now},
			ledger.Entry{Kind: ledger.KindStepSummary, Body: "summary", CreatedAt: now},
		),
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
