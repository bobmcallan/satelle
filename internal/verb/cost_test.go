package verb_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

func TestStoryEstimateWritesTagsInAnyUnit(t *testing.T) {
	wire(t)

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "tags": []string{"area:web"}}), &it)

	// The legacy units still write, preserving area:web.
	var est workitem.Item
	json.Unmarshal(call(t, "story-estimate", map[string]any{"id": it.ID, "time": "30m", "tokens": 50000, "basis": "rough"}), &est)
	if !hasTag(est.Tags, "estimate-minutes:30") || !hasTag(est.Tags, "estimate-tokens:50000") {
		t.Fatalf("estimate tags missing: %v", est.Tags)
	}
	if !hasTag(est.Tags, "area:web") {
		t.Errorf("estimate dropped an unrelated tag: %v", est.Tags)
	}

	// The current units — usd, fresh-input, output — write alongside the legacy
	// ones, never converted from/into them (sty_8eae81ac AC7).
	var cur workitem.Item
	json.Unmarshal(call(t, "story-estimate", map[string]any{"id": it.ID, "usd": 2.5, "fresh_input": 100000, "output": 20000}), &cur)
	for _, want := range []string{"estimate-usd:2.5", "estimate-fresh-input:100000", "estimate-output:20000", "estimate-minutes:30", "estimate-tokens:50000"} {
		if !hasTag(cur.Tags, want) {
			t.Errorf("after usd/fresh-input/output estimate, missing tag %q in %v", want, cur.Tags)
		}
	}

	// Re-recording an estimate replaces the prior value rather than duplicating it.
	var re workitem.Item
	json.Unmarshal(call(t, "story-estimate", map[string]any{"id": it.ID, "tokens": 60000}), &re)
	count := 0
	for _, tg := range re.Tags {
		if len(tg) >= 15 && tg[:15] == "estimate-tokens" {
			count++
		}
	}
	if count != 1 || !hasTag(re.Tags, "estimate-tokens:60000") {
		t.Errorf("re-record should replace estimate-tokens (one, =60000), got %v", re.Tags)
	}

	// Every recording left an estimate_recorded ledger row.
	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindEstimateRecorded}), &entries)
	if len(entries) != 3 {
		t.Errorf("want 3 estimate_recorded rows, got %d", len(entries))
	}
}

// TestStoryActualComputesFromLedgerNotHandEntered pins AC1: the actual is
// derived from the ledger's own dispatch/driver rows, and a computed figure
// always replaces a stale hand-typed actual-* tag — the sty_218fb3a3
// regression (an actual that stayed pinned at the 200k estimate over a 10.4M
// measured story).
func TestStoryActualComputesFromLedgerNotHandEntered(t *testing.T) {
	db := wire(t)
	ctx := context.Background()

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "actual-from-ledger",
		"tags":  []string{"area:web", "actual-tokens:200000"}, // stale hand-typed figure
	}), &it)

	cost := 0.42
	payload, _ := json.Marshal(map[string]any{
		"from": "plan", "to": "in_progress", "agent": "coder",
		"tokens_in": 1000, "tokens_out": 200, "tokens_total": 1200,
		"usage_available": true, "tokens_in_fresh": 1000,
		"duration_ms": 5000, "cost_usd": cost,
	})
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: it.ID, Kind: ledger.KindAgentInvocation, Actor: "coder", Payload: payload,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	var act workitem.Item
	json.Unmarshal(call(t, "story-actual", map[string]any{"id": it.ID}), &act)
	if !hasTag(act.Tags, "actual-tokens:1200") {
		t.Errorf("computed actual-tokens missing/wrong: %v", act.Tags)
	}
	if hasTag(act.Tags, "actual-tokens:200000") {
		t.Errorf("stale hand-typed actual-tokens survived: %v", act.Tags)
	}
	if !hasTag(act.Tags, "actual-usd:0.42") {
		t.Errorf("computed actual-usd missing/wrong: %v", act.Tags)
	}
	if !hasTag(act.Tags, "area:web") {
		t.Errorf("actual dropped an unrelated tag: %v", act.Tags)
	}

	var actuals []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindActualRecorded}), &actuals)
	if len(actuals) != 1 {
		t.Fatalf("want 1 actual_recorded row, got %d", len(actuals))
	}
	var recorded verb.StoryActual
	if err := json.Unmarshal(actuals[0].Payload, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.Total.CostUSD != cost || recorded.Total.FreshInput != 1000 || recorded.Total.Output != 200 {
		t.Errorf("actual_recorded payload = %+v, want cost %v fresh 1000 output 200", recorded.Total, cost)
	}
}

// TestStoryActualUnpricedRowsTagUnavailable pins revision 2 point 1: a story
// whose every dispatch row carries no cost_usd (a codex-style driver whose
// adapter never reports a price) must never be tagged actual-usd:0 — that
// claims a free story rather than an unmeasured one.
func TestStoryActualUnpricedRowsTagUnavailable(t *testing.T) {
	db := wire(t)
	ctx := context.Background()

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "codex-driven"}), &it)

	payload, _ := json.Marshal(map[string]any{
		"from": "plan", "to": "in_progress", "agent": "coder",
		"usage_available": true, "tokens_in_fresh": 1000, "tokens_out": 200, "duration_ms": 5000,
		// no cost_usd — the adapter never reported a price.
	})
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: it.ID, Kind: ledger.KindAgentInvocation, Actor: "coder", Payload: payload,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	var act workitem.Item
	json.Unmarshal(call(t, "story-actual", map[string]any{"id": it.ID}), &act)
	if !hasTag(act.Tags, "actual-usd:unavailable") {
		t.Errorf("all-unpriced story must be tagged actual-usd:unavailable: %v", act.Tags)
	}
	if hasTag(act.Tags, "actual-usd:0") {
		t.Errorf("all-unpriced story must never be tagged actual-usd:0: %v", act.Tags)
	}
	if !hasTag(act.Tags, "actual-cost-unavailable-rows:1") {
		t.Errorf("want actual-cost-unavailable-rows:1, got %v", act.Tags)
	}
}

// TestStoryActualUnsplitInputTagged pins revision 2 point 2: a legacy row
// recorded before the fresh/cache split existed must not vanish from the
// actual — it is tagged actual-unsplit-input and folded into actual-tokens,
// and actual-fresh-input reads "unavailable" rather than a false 0 when every
// input row is unsplit.
func TestStoryActualUnsplitInputTagged(t *testing.T) {
	db := wire(t)
	ctx := context.Background()

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "unsplit"}), &it)

	payload, _ := json.Marshal(map[string]any{
		"from": "plan", "to": "in_progress", "agent": "coder",
		"usage_available": true, "tokens_in": 300, "tokens_out": 50, "duration_ms": 1000, "cost_usd": 0.05,
		// no tokens_in_fresh/tokens_cache_write/tokens_cache_read — unsplit.
	})
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: it.ID, Kind: ledger.KindAgentInvocation, Actor: "coder", Payload: payload,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	var act workitem.Item
	json.Unmarshal(call(t, "story-actual", map[string]any{"id": it.ID}), &act)
	if !hasTag(act.Tags, "actual-unsplit-input:300") {
		t.Errorf("want actual-unsplit-input:300, got %v", act.Tags)
	}
	if !hasTag(act.Tags, "actual-fresh-input:unavailable") {
		t.Errorf("an all-unsplit input must read actual-fresh-input:unavailable, not 0: %v", act.Tags)
	}
	if !hasTag(act.Tags, "actual-tokens:350") {
		t.Errorf("want actual-tokens:350 (unsplit input folded in), got %v", act.Tags)
	}
}

// TestStoryActualRefusesCallerFigures pins AC1: story-actual takes no
// figures — it is computed, never hand-entered.
func TestStoryActualRefusesCallerFigures(t *testing.T) {
	wire(t)
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x"}), &it)
	for _, req := range []map[string]any{
		{"id": it.ID, "tokens": 200000},
		{"id": it.ID, "time": "30m"},
		{"id": it.ID, "usd": 1.0},
		{"id": it.ID, "fresh_input": 100},
		{"id": it.ID, "output": 100},
	} {
		if _, err := dispatchRaw(t, "story-actual", req); err == nil {
			t.Errorf("story-actual with %v should be refused", req)
		} else if !strings.Contains(err.Error(), "computed from the ledger") {
			t.Errorf("story-actual with %v: err = %v, want it to name the computed replacement", req, err)
		}
	}
}

func TestStoryEstimateRequiresAValue(t *testing.T) {
	wire(t)
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x"}), &it)
	if _, err := dispatchRaw(t, "story-estimate", map[string]any{"id": it.ID}); err == nil {
		t.Error("estimate with neither tokens nor time should error")
	}
}

// sty_38915987 AC3: drive refusal THROUGH story-estimate / story-actual.
// Move status between the verb's Get and Update via SetAfterTagCASGetHook so
// the test fails if ExpectStatus is removed from storyEstimate.
func TestStoryEstimateRefusesWhenStatusMoved(t *testing.T) {
	db := wire(t)
	ctx := context.Background()
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "race-est", "tags": []string{"keep-me"},
	}), &it)

	verb.SetAfterTagCASGetHook(func(c context.Context, id, statusAtGet string) {
		if statusAtGet != workitem.StatusBacklog {
			t.Errorf("estimate Get saw status %q, want backlog", statusAtGet)
		}
		if _, err := db.Stories.SetStatus(c, id, "in_progress", time.Now()); err != nil {
			t.Errorf("SetStatus under estimate: %v", err)
		}
	})
	t.Cleanup(func() { verb.SetAfterTagCASGetHook(nil) })

	_, err := dispatchRaw(t, "story-estimate", map[string]any{"id": it.ID, "time": "30m"})
	if !errors.Is(err, workitem.ErrStatusConflict) {
		t.Fatalf("story-estimate err = %v, want ErrStatusConflict", err)
	}
	got, gerr := db.Stories.Get(ctx, it.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if got.Status != "in_progress" {
		t.Errorf("status = %q after refused estimate, want in_progress", got.Status)
	}
	if hasTag(got.Tags, "estimate-minutes:30") {
		t.Errorf("refused estimate leaked tag: %v", got.Tags)
	}
	if !hasTag(got.Tags, "keep-me") {
		t.Errorf("unrelated tags changed: %v", got.Tags)
	}
}

// sty_38915987 AC3: story-actual must refuse the same Get→Update race — even
// though the actual is now computed rather than passed in (sty_8eae81ac AC1),
// recordActual's own Get→Update still goes through the same CAS.
func TestStoryActualRefusesWhenStatusMoved(t *testing.T) {
	db := wire(t)
	ctx := context.Background()
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "race-act", "tags": []string{"keep-me"},
	}), &it)

	verb.SetAfterTagCASGetHook(func(c context.Context, id, statusAtGet string) {
		if statusAtGet != workitem.StatusBacklog {
			t.Errorf("actual Get saw status %q, want backlog", statusAtGet)
		}
		if _, err := db.Stories.SetStatus(c, id, "in_progress", time.Now()); err != nil {
			t.Errorf("SetStatus under actual: %v", err)
		}
	})
	t.Cleanup(func() { verb.SetAfterTagCASGetHook(nil) })

	_, err := dispatchRaw(t, "story-actual", map[string]any{"id": it.ID})
	if !errors.Is(err, workitem.ErrStatusConflict) {
		t.Fatalf("story-actual err = %v, want ErrStatusConflict", err)
	}
	got, gerr := db.Stories.Get(ctx, it.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if got.Status != "in_progress" {
		t.Errorf("status = %q after refused actual, want in_progress", got.Status)
	}
	if hasTag(got.Tags, "actual-minutes:45") {
		t.Errorf("refused actual leaked tag: %v", got.Tags)
	}
	if !hasTag(got.Tags, "keep-me") {
		t.Errorf("unrelated tags changed: %v", got.Tags)
	}
}

// sty_ef8a896b AC2: the unitless and suffixed paths both land through
// parseCostDuration, so `--time 38` records 38 minutes on an estimate, while a
// malformed value is refused with a usable example.
func TestStoryCostTimeAcceptsUnitlessMinutes(t *testing.T) {
	wire(t)
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "units"}), &it)

	var est workitem.Item
	json.Unmarshal(call(t, "story-estimate", map[string]any{"id": it.ID, "time": "38"}), &est)
	if !hasTag(est.Tags, "estimate-minutes:38") {
		t.Errorf(`estimate --time "38" tags = %v, want estimate-minutes:38`, est.Tags)
	}
	// The suffixed path is unchanged.
	var suffixed workitem.Item
	json.Unmarshal(call(t, "story-estimate", map[string]any{"id": it.ID, "time": "2h"}), &suffixed)
	if !hasTag(suffixed.Tags, "estimate-minutes:120") {
		t.Errorf(`estimate --time "2h" tags = %v, want estimate-minutes:120`, suffixed.Tags)
	}

	_, err := dispatchRaw(t, "story-estimate", map[string]any{"id": it.ID, "time": "38x"})
	if err == nil {
		t.Fatal(`estimate --time "38x" should error`)
	}
	for _, want := range []string{"30m", "38"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should carry the example %q", err, want)
		}
	}
}
