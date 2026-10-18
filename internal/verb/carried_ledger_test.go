package verb_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// sty_59720b8b: a carried accept is a review_accept row naming the attempt it
// re-issues, with no agent_invocation row — nothing was dispatched for it.
func TestCarriedAcceptIsLedgeredWithoutAnInvocation(t *testing.T) {
	dec := verb.GateDecision{
		Gated: true, Accept: false, Skill: "satelle-a-review",
		Reviewers: []verb.ReviewerVerdict{
			{Skill: "satelle-a-review", Order: 0, Accept: false, Notes: "objection", Command: "claude -p", Context: "satelle-a-review"},
			{Skill: "satelle-b-review", Order: 1, Accept: true, Reviewed: "PLAN-B", CarriedFrom: 3,
				Notes: "carried from attempt 3: quotation unchanged"},
		},
	}
	lg, id := bundleTransition(t, dec)

	invs, err := lg.ListByStory(context.Background(), id, ledger.KindAgentInvocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(invs) != 1 {
		t.Fatalf("only the dispatched reviewer has an invocation row, got %d: %+v", len(invs), invs)
	}
	accepts, _ := lg.ListByStory(context.Background(), id, ledger.KindReviewAccept)
	if len(accepts) != 1 {
		t.Fatalf("accepts = %+v", accepts)
	}
	var row struct {
		Skill       string `json:"skill"`
		Reviewed    string `json:"reviewed"`
		CarriedFrom int    `json:"carried_from"`
	}
	if err := json.Unmarshal(accepts[0].Payload, &row); err != nil {
		t.Fatal(err)
	}
	if row.Skill != "satelle-b-review" || row.CarriedFrom != 3 || row.Reviewed != "PLAN-B" {
		t.Errorf("carried accept row = %+v", row)
	}
	rejects, _ := lg.ListByStory(context.Background(), id, ledger.KindReviewReject)
	var fresh struct {
		CarriedFrom int `json:"carried_from"`
	}
	if len(rejects) != 1 || json.Unmarshal(rejects[0].Payload, &fresh) != nil || fresh.CarriedFrom != 0 {
		t.Errorf("a fresh verdict carries no carried_from: %+v", rejects)
	}
}
