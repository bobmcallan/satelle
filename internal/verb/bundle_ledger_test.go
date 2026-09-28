package verb_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_23e10d92: a bundled reviewer session is ONE measured invocation judging
// several rubrics. The ledger records one agent_invocation row for it — with the
// call's usage — and one review_accept/review_reject row per rubric, each under
// its own skill and none carrying usage.

func bundledDecision(usageAvailable bool) verb.GateDecision {
	skills := []string{"satelle-a-review", "satelle-b-review", "satelle-c-review"}
	cost := 0.30
	mk := func(i int, accept bool) verb.ReviewerVerdict {
		rv := verb.ReviewerVerdict{
			Skill: skills[i], Order: i, Accept: accept, Notes: "notes " + skills[i],
			Command: "claude -p", Context: skills[i], Model: "opus", ModelResolved: "claude-opus-5-5",
			BundleID: "bun_test", BundleSkills: skills,
		}
		if i == 0 { // the session's usage rides its first verdict alone
			rv.UsageAvailable = usageAvailable
			if usageAvailable {
				rv.TokensIn, rv.TokensOut, rv.TokensTotal = 1000, 100, 1100
				rv.TokensInFresh = 1000
				rv.CostUSD = &cost
			} else {
				rv.UsageUnavailableReason = "grok adapter: --output-format json envelope carries no usage object"
				rv.CostUnavailableReason = "grok adapter: no cost reported"
			}
		}
		return rv
	}
	return verb.GateDecision{
		Gated: true, Accept: false, Skill: skills[1],
		Reviewers: []verb.ReviewerVerdict{mk(0, true), mk(1, false), mk(2, true)},
	}
}

func bundleTransition(t *testing.T, dec verb.GateDecision) (*ledger.Store, string) {
	t.Helper()
	db := wire(t)
	verb.SetTransitionGater(stubGater{dec: dec})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "in_progress"}), &it); err != nil {
		t.Fatal(err)
	}
	_, _ = dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "done"})
	return db.Ledger, it.ID
}

func TestBundledGateWritesOneInvocationRowAndOneVerdictRowPerRubric(t *testing.T) {
	lg, id := bundleTransition(t, bundledDecision(true))

	invs, err := lg.ListByStory(context.Background(), id, ledger.KindAgentInvocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(invs) != 1 {
		t.Fatalf("a bundle of 3 is ONE invocation row, got %d: %+v", len(invs), invs)
	}
	var inv struct {
		Skill        string   `json:"skill"`
		BundleID     string   `json:"bundle_id"`
		BundleSkills []string `json:"bundle_skills"`
		TokensTotal  int      `json:"tokens_total"`
	}
	if err := json.Unmarshal(invs[0].Payload, &inv); err != nil {
		t.Fatal(err)
	}
	if inv.BundleID != "bun_test" || len(inv.BundleSkills) != 3 || inv.TokensTotal != 1100 {
		t.Errorf("invocation row = %+v", inv)
	}
	if inv.Skill != "satelle-a-review+satelle-b-review+satelle-c-review" {
		t.Errorf("the row names every rubric it judged, got skill %q", inv.Skill)
	}

	accepts, _ := lg.ListByStory(context.Background(), id, ledger.KindReviewAccept)
	rejects, _ := lg.ListByStory(context.Background(), id, ledger.KindReviewReject)
	if len(accepts) != 2 || len(rejects) != 1 {
		t.Fatalf("one verdict row per rubric: %d accepts, %d rejects", len(accepts), len(rejects))
	}
	seen := map[string]bool{}
	for _, e := range append(accepts, rejects...) {
		var row struct {
			Skill      string `json:"skill"`
			Notes      string `json:"notes"`
			BundleID   string `json:"bundle_id"`
			BundleSize int    `json:"bundle_size"`
			TokensIn   int    `json:"tokens_in"`
		}
		if err := json.Unmarshal(e.Payload, &row); err != nil {
			t.Fatal(err)
		}
		if row.BundleID != "bun_test" || row.BundleSize != 3 {
			t.Errorf("verdict row must name its bundle: %+v", row)
		}
		if row.Notes != "notes "+row.Skill {
			t.Errorf("each verdict keeps its own notes: %+v", row)
		}
		if row.TokensIn != 0 {
			t.Errorf("a verdict row carries no usage — it is on the invocation row: %+v", row)
		}
		seen[row.Skill] = true
	}
	if len(seen) != 3 {
		t.Errorf("each rubric recorded under its own skill: %v", seen)
	}
}

// TestBundledGateAllocationIsLabelledAndSumsToTheMeasuredRow drives the ledger a
// bundle wrote through the gate-value view: every rubric gets a share, each
// share is labelled an allocation, and the shares add back to the ONE measured
// row rather than adding a second measured call.
func TestBundledGateAllocationIsLabelledAndSumsToTheMeasuredRow(t *testing.T) {
	lg, id := bundleTransition(t, bundledDecision(true))
	entries, err := lg.ListByStory(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	report := costview.GateValue(map[string][]ledger.Entry{id: entries}, costview.GateFilter{})
	if len(report.Rows) != 3 {
		t.Fatalf("one row per rubric, got %+v", report.Rows)
	}
	var fresh int
	var cost float64
	for _, r := range report.Rows {
		if r.Invocations != 0 {
			t.Errorf("an allocated share is not a measured invocation: %+v", r)
		}
		if r.AllocatedRows != 1 || r.AllocationNote != "allocated share of bundle bun_test" {
			t.Errorf("share must be labelled an allocation: %+v", r)
		}
		fresh += r.FreshTokens
		cost += r.CostUSD
	}
	if fresh != 1000 {
		t.Errorf("shares must sum to the measured 1000 fresh tokens, got %d", fresh)
	}
	if d := cost - 0.30; d > 1e-9 || d < -1e-9 {
		t.Errorf("shares must sum to the measured $0.30, got %v", cost)
	}
	// Verdicts still land beside the spend, per rubric.
	for _, r := range report.Rows {
		if r.Accepts+r.Rejects != 1 {
			t.Errorf("each rubric has its own verdict: %+v", r)
		}
	}
}

func TestBundledGateUnavailableUsageStaysUnavailableInEveryShare(t *testing.T) {
	lg, id := bundleTransition(t, bundledDecision(false))
	entries, _ := lg.ListByStory(context.Background(), id, "")
	report := costview.GateValue(map[string][]ledger.Entry{id: entries}, costview.GateFilter{})
	for _, r := range report.Rows {
		if r.UsageRows != 0 || r.UsageUnavailableRows != 1 || r.Costed != 0 || r.Uncosted != 1 {
			t.Errorf("unavailable usage must not read as zero in any share: %+v", r)
		}
		if r.AllocationUnavailableReason == "" {
			t.Errorf("the adapter's reason must ride the share: %+v", r)
		}
	}
}
