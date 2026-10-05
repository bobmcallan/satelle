package agentstep

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestReviewedCeiling(t *testing.T) {
	if reviewedCeiling != 8<<10 {
		t.Fatalf("reviewedCeiling = %d, want 8192", reviewedCeiling)
	}
	at := strings.Repeat("Y", reviewedCeiling)
	over := strings.Repeat("Z", reviewedCeiling+1)

	t.Run("parseDecision", func(t *testing.T) {
		kept, err := parseDecision([]byte(`{"decision":"accept","notes":"at the ceiling","reviewed":"` + at + `"}`))
		if err != nil {
			t.Fatal(err)
		}
		if kept.Reviewed != at || kept.ReviewedTruncated {
			t.Fatalf("at ceiling: reviewed len %d truncated %v", len(kept.Reviewed), kept.ReviewedTruncated)
		}
		dropped, err := parseDecision([]byte(`{"decision":"reject","notes":"over the ceiling","reviewed":"` + over + `"}`))
		if err != nil {
			t.Fatal(err)
		}
		if dropped.Reviewed != "" || !dropped.ReviewedTruncated {
			t.Fatalf("over ceiling: reviewed %q truncated %v", dropped.Reviewed, dropped.ReviewedTruncated)
		}
		if strings.Contains(dropped.Reviewed, "Z") || strings.Contains(dropped.Notes, "Z") {
			t.Fatal("over-ceiling quotation must not be stored, even as a prefix")
		}
	})

	t.Run("parseBundleDecisions", func(t *testing.T) {
		kept, err := parseBundleDecisions([]byte(`{"verdicts":[{"skill":"rev-a","decision":"accept","notes":"at","reviewed":"`+at+`"}]}`), []string{"rev-a"})
		if err != nil {
			t.Fatal(err)
		}
		if kept[0].Reviewed != at || kept[0].ReviewedTruncated {
			t.Fatalf("bundle at ceiling: %+v", kept[0])
		}
		dropped, err := parseBundleDecisions([]byte(`{"verdicts":[{"skill":"rev-a","decision":"reject","notes":"over","reviewed":"`+over+`"}]}`), []string{"rev-a"})
		if err != nil {
			t.Fatal(err)
		}
		if dropped[0].Reviewed != "" || !dropped[0].ReviewedTruncated {
			t.Fatalf("bundle over ceiling: %+v", dropped[0])
		}
	})
}

// TestSecondPresentationInvokesReviewer (AC2): a prior accept that already
// carries reviewed does not skip the reviewer. The binary does not decide
// that the material is unchanged.
func TestSecondPresentationInvokesReviewer(t *testing.T) {
	g, r := newEngine(t, `{"decision":"accept","notes":"again"}`, fakeDocs{
		workflow: planEdgeWorkflow, skillBody: "rubric", skillFound: true,
	})
	g.SetPriorVerdictsResolver(func(context.Context, string, string, string) []PriorVerdict {
		return []PriorVerdict{{
			Skill:    "satelle-story-plan-review",
			Decision: "accept",
			Notes:    "earlier accept",
			Reviewed: "the words already judged",
		}}
	})
	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_again", Status: "plan"}, "in_progress"); err != nil {
		t.Fatal(err)
	}
	if r.got.Payload == "" {
		t.Fatal("a re-presented edge must invoke the reviewer again")
	}
}
