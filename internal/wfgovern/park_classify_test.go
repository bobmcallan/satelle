package wfgovern

import (
	"testing"
)

// TestDerivedParkClassifiesAndStamps is AC1 of sty_524f091d: the reporter's
// exact park declaration derives a Spec where blocked is a park, and entering
// it from in_progress is a stamp (not a classification miss).
func TestDerivedParkClassifiesAndStamps(t *testing.T) {
	rs := RouteSource{
		Done: `["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked", gate = "satelle-story-blocked-review", advisor = "blocked-triage", advisor_skill = "satelle-story-blocked-triage" }
cancel = { state = "cancelled", gate = "satelle-story-cancel-review" }
`,
		Step: `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`,
	}
	got, err := RouteSpecFor(rs, "fix", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Spec.IsParkState("blocked") {
		t.Fatal("derived blocked must be IsParkState — classification is not the wipe")
	}
	if !got.Spec.IsResumePark("blocked") {
		t.Fatal("derived blocked must be the from=* resume park")
	}
	if got.Spec.IsResumePark("cancelled") {
		t.Fatal("cancelled is a sink, not a resume park")
	}
}
