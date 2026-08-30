package verb_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
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

func TestStoryEstimateAndActualRecordTagsAndLedger(t *testing.T) {
	wire(t)

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "tags": []string{"area:web"}}), &it)

	// Estimate writes estimate-minutes/estimate-tokens, preserving area:web.
	var est workitem.Item
	json.Unmarshal(call(t, "story-estimate", map[string]any{"id": it.ID, "time": "30m", "tokens": 50000, "basis": "rough"}), &est)
	if !hasTag(est.Tags, "estimate-minutes:30") || !hasTag(est.Tags, "estimate-tokens:50000") {
		t.Fatalf("estimate tags missing: %v", est.Tags)
	}
	if !hasTag(est.Tags, "area:web") {
		t.Errorf("estimate dropped an unrelated tag: %v", est.Tags)
	}

	// Actual writes actual-* and leaves the estimate-* tags intact.
	var act workitem.Item
	json.Unmarshal(call(t, "story-actual", map[string]any{"id": it.ID, "time": "50m", "tokens": 95000}), &act)
	for _, want := range []string{"actual-minutes:50", "actual-tokens:95000", "estimate-tokens:50000", "area:web"} {
		if !hasTag(act.Tags, want) {
			t.Errorf("after actual, missing tag %q in %v", want, act.Tags)
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

	// Both recordings left ledger rows of the right kinds.
	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindEstimateRecorded}), &entries)
	if len(entries) != 2 {
		t.Errorf("want 2 estimate_recorded rows, got %d", len(entries))
	}
	var actuals []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindActualRecorded}), &actuals)
	if len(actuals) != 1 {
		t.Errorf("want 1 actual_recorded row, got %d", len(actuals))
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

// sty_ef8a896b AC2: the unitless and suffixed paths both land through the one
// recordCost funnel, so `--time 38` records 38 minutes on estimate AND actual,
// while a malformed value is refused with a usable example.
func TestStoryCostTimeAcceptsUnitlessMinutes(t *testing.T) {
	wire(t)
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "units"}), &it)

	var est workitem.Item
	json.Unmarshal(call(t, "story-estimate", map[string]any{"id": it.ID, "time": "38"}), &est)
	if !hasTag(est.Tags, "estimate-minutes:38") {
		t.Errorf(`estimate --time "38" tags = %v, want estimate-minutes:38`, est.Tags)
	}
	var act workitem.Item
	json.Unmarshal(call(t, "story-actual", map[string]any{"id": it.ID, "time": "38"}), &act)
	if !hasTag(act.Tags, "actual-minutes:38") {
		t.Errorf(`actual --time "38" tags = %v, want actual-minutes:38`, act.Tags)
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
