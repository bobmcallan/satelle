package config

import (
	"strings"
	"testing"
)

// AC2: the embedded substrate-review rubric directs each judge to remove what
// a default covers or nothing reads, reduce history narration and methodology
// description (apply, not describe), flag contradictions with file:line, and
// give replacement text for every reduce.
func TestSubstrateReviewSkillDirectives(t *testing.T) {
	body := embeddedBody(t, "skills", "satelle-substrate-review")
	for _, want := range []string{
		"**remove** when an embedded default covers it",
		"or when\n  nothing reads it",
		"cite the search that found no reader",
		"**reduce** to the lines no default carries",
		"Cut history narration",
		"methodology description",
		"applies a practice, it does not describe one",
		"[[satelle-story-shape]]",
		"[[satelle-start-small]]",
		"**conflict** when the text contradicts the code or a default",
		"`file:line`",
		"`replacement`; a reduce without it is not counted",
		"`missed_defaults`",
		"Every disposition needs `reason` and `evidence`",
		"never as keep",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the substrate-review rubric lacks %q", want)
		}
	}
	if strings.Contains(body, "satelle doc get") || strings.Contains(body, "substrate-review-input") {
		t.Errorf("the rubric still points the judge at embedded input or a command its read grant cannot run")
	}
}
