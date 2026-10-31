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

// storyShapeRules are the three story-shape rules (sty_bcd344fd AC3), each as
// the phrases a review body must carry together: the card is a behaviour, every
// AC traces to it and is provable at its gate, and the slice is the smallest
// that proves it. Each rule names the principle it enforces. The operator-config
// test TestOperatorReadyReviewEnforcesStoryShape (tests/) holds this repo's
// ready-review override to the same phrases; it cannot import a _test table.
var storyShapeRules = map[string][]string{
	"card is a behaviour":           {"[[satelle-story-shape]]", "not a behaviour"},
	"AC traceable and provable":     {"traceable", "provable at"},
	"smallest slice that proves it": {"[[satelle-start-small]]", "smallest slice"},
}

// missingStoryShapeRules lists each rule whose phrases body does not carry,
// with line wrapping ignored.
func missingStoryShapeRules(body string) []string {
	flat := strings.Join(strings.Fields(body), " ")
	var missing []string
	for rule, phrases := range storyShapeRules {
		for _, p := range phrases {
			if !strings.Contains(flat, p) {
				missing = append(missing, rule+": "+p)
			}
		}
	}
	return missing
}

// TestReviewsEnforceStoryShape (sty_bcd344fd AC3): the embedded plan gate and
// the embedded container readiness rubric both reject a story whose card is not
// a behaviour, whose ACs are not each traceable and provable at their gate, or
// whose slice is not the smallest that proves the behaviour.
func TestReviewsEnforceStoryShape(t *testing.T) {
	bodies := map[string]string{}
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" {
			bodies[d.Name] = d.Body
		}
	}
	for _, name := range []string{"satelle-story-plan-review", "ready"} {
		body, ok := bodies[name]
		if !ok {
			t.Errorf("embedded skill %s missing", name)
			continue
		}
		for _, m := range missingStoryShapeRules(body) {
			t.Errorf("%s does not carry the story-shape rule %s", name, m)
		}
	}
}
