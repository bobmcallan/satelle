package wfdot

import (
	"strings"
	"testing"
)

// A gate's optional `when` names a functional-check skill. It is parsed, carried
// to the scoped-reviewer view the engine and the route preview read, and absent
// on a gate that declares none.
func TestGateWhenIsParsedAndCarried(t *testing.T) {
	done, step := fixtures(t)
	step += "\n[[gate]]\nskill = \"rev-when\"\nagent = \"reviewer\"\non = [\"integration\"]\nwhen = \" trig \"\n"
	spec, err := ParseRoute(done, step, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	var got *ScopedReviewer
	enq, _ := spec.ScopedReviewersSplit("integration", nil)
	for i := range enq {
		if enq[i].Skill == "rev-when" {
			got = &enq[i]
		} else if enq[i].When != "" {
			t.Errorf("gate %s carries when=%q but declared none", enq[i].Skill, enq[i].When)
		}
	}
	if got == nil {
		t.Fatalf("rev-when not enqueued on integration: %v", enq)
	}
	if got.When != "trig" {
		t.Errorf("When = %q, want the trimmed skill name", got.When)
	}
	if other, _ := spec.ScopedReviewersSplit("release", nil); len(other) > 0 {
		for _, s := range other {
			if s.Skill == "rev-when" {
				t.Errorf("rev-when must only gate integration, got it on release")
			}
		}
	}
}

// A misspelt key is an unknown-key error, never a silently dropped precondition.
func TestGateWhenTypoIsRefused(t *testing.T) {
	done, step := fixtures(t)
	step += "\n[[gate]]\nskill = \"rev-when\"\non = [\"integration\"]\nwhn = \"trig\"\n"
	if _, err := ParseRoute(done, step, "feature", nil); err == nil || !strings.Contains(err.Error(), "whn") {
		t.Fatalf("want an unknown-key error naming whn, got %v", err)
	}
}
