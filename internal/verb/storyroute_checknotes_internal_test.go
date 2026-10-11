package verb

import (
	"strings"
	"testing"
	"time"
)

// sty_e2b78a8d: a functional check's notes are now a short label plus the
// check's output. Ledger rows recorded before that hold the whole script in the
// notes; they are history, shown as stored, and must still render beside the new
// form.
func TestRenderOutcome_NewAndLegacyFunctionalCheckNotes(t *testing.T) {
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	newForm := ReviewerVerdict{Skill: "gate-new", Accept: true, Notes: "functional check passed (gate-new)\nresult-line"}
	legacy := ReviewerVerdict{Skill: "gate-old", Accept: true, Notes: "functional check passed: `set -e\necho the-old-script`"}

	got := renderOutcome("sty_x", "plan", "in_progress", []ReviewerVerdict{newForm, legacy}, nil, now)
	for _, want := range []string{
		"**gate-new** — ACCEPT",
		"functional check passed (gate-new) result-line",
		"**gate-old** — ACCEPT",
		"the-old-script",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("outcome lacks %q:\n%s", want, got)
		}
	}
}
