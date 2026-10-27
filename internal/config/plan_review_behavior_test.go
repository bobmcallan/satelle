package config

import (
	"strings"
	"testing"
)

// TestPlanReviewJudgesAgainstCard (sty_97c88302): the embedded plan gate judges
// the plan against the behavior the story card states — each criterion cites
// the card behavior it proves, the set is not narrower than the card, and a
// partial solution recorded as context is kept — and carries the generic
// discovery judgments without any repo-only heading or principle.
func TestPlanReviewJudgesAgainstCard(t *testing.T) {
	var body string
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == "satelle-story-plan-review" {
			body = d.Body
			break
		}
	}
	if body == "" {
		t.Fatal("embedded plan-review skill missing")
	}
	for _, want := range []string{
		"cite the card behavior it proves",
		"narrower than the card",
		"partial solution",
		"as context",
		"do not replace the card",
		"criteria_source",
		"Traced",
		"Testable",
		"Outcome, not mechanism",
		"Faithful to the card",
		"Proof path",
		"Root cause",
		"fail-fast",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("plan-review missing phrase %q", want)
		}
	}
	for _, gone := range []string{
		"Premise check",
		"satelle-discovery-criteria",
	} {
		if strings.Contains(body, gone) {
			t.Errorf("plan-review names repo-only %q", gone)
		}
	}
}
