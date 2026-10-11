package costview_test

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Hand-priced fixtures (USD per million tokens). The ids are test data, not a
// price table the product carries — the rates always come from configuration.
func ratesPricing() costview.Pricing {
	return costview.Pricing{Models: map[string]costview.Rates{
		"m-big":   {FreshInput: 3, CacheRead: 0.3, CacheWrite: 3.75, Output: 15},
		"m-small": {FreshInput: 1, CacheRead: 0.1, CacheWrite: 1.25, Output: 5},
	}}
}

func rawInvocation(t *testing.T, storyID string, at time.Time, payload map[string]any) ledger.Entry {
	t.Helper()
	if _, ok := payload["usage_available"]; !ok {
		payload["usage_available"] = true
	}
	return ledger.Entry{StoryID: storyID, Kind: ledger.KindAgentInvocation, Payload: mustJSON(t, payload), CreatedAt: at}
}

func driverEntry(t *testing.T, storyID string, at time.Time, d map[string]any) ledger.Entry {
	t.Helper()
	return ledger.Entry{StoryID: storyID, Kind: ledger.KindDriverUsage, Payload: mustJSON(t, d), CreatedAt: at}
}

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func seatByName(t *testing.T, lines []costview.SeatLine, seat string) costview.SeatLine {
	t.Helper()
	for _, l := range lines {
		if l.Seat == seat {
			return l
		}
	}
	t.Fatalf("no seat %q in %+v", seat, lines)
	return costview.SeatLine{}
}

func ownPriced(t *testing.T, entries []ledger.Entry, p costview.Pricing) costview.Story {
	t.Helper()
	return costview.Own(workitem.Item{ID: "sty_p"}, entries, fixtureClock(), time.Now(), costview.WithPricing(p))
}

// TestBySeatLinesAndCountingRule pins AC1/AC3: one line per agent+model seat,
// each from one accumulator; a bundled reviewer session counts once, whole,
// under its seat; the driving session is the driver@model seat, and a
// superseded or baseline driver row adds no tokens (and so no price).
func TestBySeatLinesAndCountingRule(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entries := []ledger.Entry{
		rawInvocation(t, "sty_p", at, map[string]any{"agent": "coder", "model": "alias", "model_resolved": "m-big", "adapter": "claude",
			"tokens_in_fresh": 1_000_000, "tokens_cache_read": 2_000_000, "tokens_in": 3_000_000, "tokens_out": 100_000}),
		rawInvocation(t, "sty_p", at, map[string]any{"agent": "reviewer", "model_resolved": "m-small", "adapter": "claude",
			"bundle_id": "b1", "bundle_skills": []string{"s-one", "s-two"},
			"tokens_in_fresh": 500_000, "tokens_in": 500_000, "tokens_out": 200_000}),
		// driver: an unavailable attempt later recovered by a backfill (superseded),
		// a measured window, a fresh baseline, and the backfill itself.
		driverEntry(t, "sty_p", at, map[string]any{"session_id": "S1", "executable": "claude", "model": "m-big", "available": false}),
		driverEntry(t, "sty_p", at, map[string]any{"session_id": "S2", "executable": "claude", "model": "m-big", "available": true,
			"fresh_input": 200_000, "cache_read": 1_000_000, "output": 50_000}),
		driverEntry(t, "sty_p", at, map[string]any{"session_id": "S3", "executable": "claude", "model": "m-big", "available": true,
			"baseline_fresh": true, "fresh_input": 999_999}),
		driverEntry(t, "sty_p", at, map[string]any{"session_id": "S1", "executable": "claude", "model": "m-big", "available": true,
			"backfilled": true, "fresh_input": 100_000, "output": 10_000}),
	}
	s := ownPriced(t, entries, ratesPricing())

	if len(s.Seats) != 3 {
		t.Fatalf("seats = %+v, want 3 (coder@m-big, reviewer@m-small, driver@m-big)", s.Seats)
	}
	coder := seatByName(t, s.Seats, "coder@m-big")
	if coder.FreshInput != 1_000_000 || coder.CacheRead != 2_000_000 || coder.Output != 100_000 || coder.CacheWrite != 0 {
		t.Errorf("coder tokens = %+v", coder)
	}
	near(t, "coder cost", coder.Derived.USD, 3+0.6+1.5) // 1M fresh, 2M read, 100k out

	rev := seatByName(t, s.Seats, "reviewer@m-small")
	if rev.UsageRows != 1 {
		t.Errorf("bundled row counted %d times, want once", rev.UsageRows)
	}
	near(t, "reviewer cost", rev.Derived.USD, 0.5+1.0)

	drv := seatByName(t, s.Seats, "driver@m-big")
	if drv.FreshInput != 300_000 || drv.CacheRead != 1_000_000 || drv.Output != 60_000 {
		t.Errorf("driver tokens = %+v (superseded and baseline rows must add nothing)", drv)
	}
	near(t, "driver cost", drv.Derived.USD, (0.6+0.3+0.75)+(0.3+0.15))

	// the story total is the sum of the seats, driver tokens included
	near(t, "total", s.Figures.Derived.USD, 5.1+1.5+2.1)
	if len(s.Figures.Derived.Unavailable) != 0 {
		t.Errorf("unexpected unavailable notes: %v", s.Figures.Derived.Unavailable)
	}
	if got := costview.FormatDerived(s.Figures.Derived); got != "$8.7000" {
		t.Errorf("FormatDerived = %q", got)
	}
	views := costview.FormatSeatLines(s.Seats)
	if len(views) != 3 || views[0].Seat != "coder@m-big" || views[0].Cost != "$5.1000" {
		t.Errorf("seat views = %+v", views)
	}
}

// TestPricePerModelEntries pins AC3's per-entry rules: each entry is priced
// under its own id with fresh = tokens_in - cache_write - cache_read; an entry
// with no reported cache split reads unavailable on a multi-entry row (never
// all-fresh), and borrows the row-level split only as the row's sole entry.
func TestPricePerModelEntries(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	multi := rawInvocation(t, "sty_p", at, map[string]any{"agent": "coder", "model_resolved": "m-big", "adapter": "claude",
		"tokens_in_fresh": 900_000, "tokens_cache_read": 1_000_000, "tokens_in": 1_900_000, "tokens_out": 150_000,
		"model_usage": []map[string]any{
			{"id": "m-big", "tokens_in": 1_500_000, "tokens_out": 100_000, "tokens_cache_read": 1_000_000},
			{"id": "m-small", "tokens_in": 400_000, "tokens_out": 50_000},
		}})
	s := ownPriced(t, []ledger.Entry{multi}, ratesPricing())
	// m-big: fresh 500k*3 + read 1M*0.3 + out 100k*15 = 1.5+0.3+1.5
	near(t, "multi-entry cost", s.Figures.Derived.USD, 3.3)
	if got := s.Figures.Derived.Unavailable; len(got) != 1 || got[0] != "model m-small: cache split not reported" {
		t.Errorf("unavailable = %v", got)
	}
	if got := costview.FormatDerived(s.Figures.Derived); got != "$3.3000 + unavailable (model m-small: cache split not reported)" {
		t.Errorf("FormatDerived = %q", got)
	}

	sole := rawInvocation(t, "sty_p", at, map[string]any{"agent": "coder", "model_resolved": "m-small", "adapter": "claude",
		"tokens_in_fresh": 100_000, "tokens_cache_write": 100_000, "tokens_cache_read": 400_000, "tokens_in": 600_000, "tokens_out": 40_000,
		"model_usage": []map[string]any{{"id": "m-small", "tokens_in": 600_000, "tokens_out": 40_000}}})
	s = ownPriced(t, []ledger.Entry{sole}, ratesPricing())
	near(t, "sole-entry cost", s.Figures.Derived.USD, 0.1+0.125+0.04+0.2)
	if len(s.Figures.Derived.Unavailable) != 0 {
		t.Errorf("sole entry should borrow the row split: %v", s.Figures.Derived.Unavailable)
	}

	plain := rawInvocation(t, "sty_p", at, map[string]any{"agent": "coder", "model_resolved": "m-big", "adapter": "claude",
		"tokens_in_fresh": 200_000, "tokens_cache_write": 40_000, "tokens_cache_read": 500_000, "tokens_in": 740_000, "tokens_out": 10_000})
	s = ownPriced(t, []ledger.Entry{plain}, ratesPricing())
	near(t, "row without entries", s.Figures.Derived.USD, 0.6+0.15+0.15+0.15)
}

// TestPriceUnavailableNeverZero pins AC5: an unknown model, an unrecorded model
// (dispatch and driver, adapter-named, including a non-Claude row), a legacy
// unsplit row and a missing configuration are each unavailable, never $0.
func TestPriceUnavailableNeverZero(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		entries []ledger.Entry
		want    string
	}{
		{"unknown model", []ledger.Entry{rawInvocation(t, "sty_p", at, map[string]any{"agent": "coder", "model_resolved": "m-ghost",
			"tokens_in_fresh": 10, "tokens_in": 10, "tokens_out": 1})}, "unavailable (model m-ghost has no rate)"},
		{"unrecorded dispatch model (grok)", []ledger.Entry{rawInvocation(t, "sty_p", at, map[string]any{"agent": "reviewer", "adapter": "grok",
			"model_resolved": "unavailable: grok command reports no model", "tokens_in_fresh": 10, "tokens_in": 10, "tokens_out": 1})},
			"unavailable (model unrecorded (adapter grok))"},
		{"unrecorded driver model", []ledger.Entry{driverEntry(t, "sty_p", at, map[string]any{"session_id": "S", "executable": "grok",
			"available": true, "fresh_input": 10, "output": 1})}, "unavailable (model unrecorded (adapter grok))"},
		{"legacy unsplit row", []ledger.Entry{rawInvocation(t, "sty_p", at, map[string]any{"agent": "coder", "model_resolved": "m-big",
			"tokens_in": 1000, "tokens_out": 100, "tokens_total": 1100})}, "unavailable (model m-big: unsplit legacy row)"},
	}
	for _, c := range cases {
		s := ownPriced(t, c.entries, ratesPricing())
		if got := costview.FormatDerived(s.Figures.Derived); got != c.want {
			t.Errorf("%s: FormatDerived = %q, want %q", c.name, got, c.want)
		}
		if s.Figures.Derived.USD != 0 || s.Figures.Derived.PricedParts != 0 {
			t.Errorf("%s: priced something it cannot: %+v", c.name, s.Figures.Derived)
		}
	}

	// no [pricing] section at all
	none := costview.Own(workitem.Item{ID: "sty_p"}, []ledger.Entry{rawInvocation(t, "sty_p", at, map[string]any{"agent": "coder",
		"model_resolved": "m-big", "tokens_in_fresh": 10, "tokens_in": 10})}, fixtureClock(), time.Now())
	if got := costview.FormatDerived(none.Figures.Derived); got != "unavailable (no pricing configured)" {
		t.Errorf("no config: %q", got)
	}
	// an unreadable [pricing] says so rather than posing as unconfigured
	bad := ownPriced(t, nil, costview.Pricing{Invalid: "pricing.subscription.fee_usd must not be negative"})
	if got := costview.FormatDerived(bad.Figures.Derived); !strings.Contains(got, "fee_usd") {
		t.Errorf("invalid config: %q", got)
	}
	// a seat on a row with an unrecorded model is plain "agent", not agent@marker
	s := ownPriced(t, []ledger.Entry{rawInvocation(t, "sty_p", at, map[string]any{"agent": "reviewer", "adapter": "grok",
		"model_resolved": "unavailable", "tokens_in_fresh": 1, "tokens_in": 1})}, ratesPricing())
	seatByName(t, s.Seats, "reviewer")
}

// TestSubscriptionShare pins AC4: fee × story_total_tokens / tokens_per_period
// over the band's class sum, shown per seat by the same rule and labelled as a
// share.
func TestSubscriptionShare(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entries := []ledger.Entry{
		rawInvocation(t, "sty_p", at, map[string]any{"agent": "coder", "model_resolved": "m-big",
			"tokens_in_fresh": 100_000, "tokens_cache_read": 150_000, "tokens_in": 250_000, "tokens_out": 50_000}),
		rawInvocation(t, "sty_p", at, map[string]any{"agent": "reviewer", "model_resolved": "m-small",
			"tokens_in": 100_000, "tokens_total": 100_000}), // legacy unsplit: counted in the class sum
		driverEntry(t, "sty_p", at, map[string]any{"session_id": "S", "executable": "claude", "model": "m-big", "available": true,
			"fresh_input": 50_000, "output": 50_000}),
	}
	p := costview.Pricing{Subscription: &costview.Subscription{FeeUSD: 20, TokensPerPeriod: 1_000_000}}
	s := ownPriced(t, entries, p)
	if s.Figures.TotalTokens() != 500_000 {
		t.Fatalf("class sum = %d, want 500000", s.Figures.TotalTokens())
	}
	near(t, "story share", s.Figures.Derived.USD, 10)
	near(t, "coder share", seatByName(t, s.Seats, "coder@m-big").Derived.USD, 20*300_000.0/1_000_000)
	near(t, "reviewer share", seatByName(t, s.Seats, "reviewer@m-small").Derived.USD, 2)
	near(t, "driver share", seatByName(t, s.Seats, "driver@m-big").Derived.USD, 2)
	if got := costview.FormatDerived(s.Figures.Derived); got != "$10.0000 ("+costview.SubscriptionShareLabel+")" {
		t.Errorf("FormatDerived = %q", got)
	}
	if s.Figures.CostBand() != "medium" {
		t.Errorf("band moved: %q", s.Figures.CostBand())
	}
}

// TestRepriceWithoutRewrite pins AC6: the same ledger entries price differently
// under two configurations on the next read, and no entry byte changes.
func TestRepriceWithoutRewrite(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entries := []ledger.Entry{
		rawInvocation(t, "sty_p", at, map[string]any{"agent": "coder", "model_resolved": "m-big",
			"tokens_in_fresh": 1_000_000, "tokens_in": 1_000_000, "tokens_out": 0}),
	}
	before := bytes.Clone(entries[0].Payload)
	cheap := costview.Pricing{Models: map[string]costview.Rates{"m-big": {FreshInput: 1}}}
	dear := costview.Pricing{Models: map[string]costview.Rates{"m-big": {FreshInput: 4}}}
	a, b := ownPriced(t, entries, cheap), ownPriced(t, entries, dear)
	near(t, "cheap", a.Figures.Derived.USD, 1)
	near(t, "dear", b.Figures.Derived.USD, 4)
	if !bytes.Equal(before, entries[0].Payload) {
		t.Errorf("pricing rewrote the ledger entry")
	}
}

// TestFamilyPricingRollsUp pins AC7: per-seat tokens and derived cost roll up
// across children, and an unavailable part propagates to the total.
func TestFamilyPricingRollsUp(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	root := workitem.Item{ID: "epic"}
	kids := []workitem.Item{{ID: "k1", ParentID: "epic"}, {ID: "k2", ParentID: "epic"}}
	entries := map[string][]ledger.Entry{
		"k1": {rawInvocation(t, "k1", at, map[string]any{"agent": "coder", "model_resolved": "m-big",
			"tokens_in_fresh": 1_000_000, "tokens_in": 1_000_000})},
		"k2": {
			rawInvocation(t, "k2", at, map[string]any{"agent": "coder", "model_resolved": "m-big",
				"tokens_in_fresh": 1_000_000, "tokens_in": 1_000_000, "tokens_out": 100_000}),
			rawInvocation(t, "k2", at, map[string]any{"agent": "reviewer", "model_resolved": "m-ghost",
				"tokens_in_fresh": 5, "tokens_in": 5}),
		},
	}
	fam := costview.Family(root, append([]workitem.Item{root}, kids...), entries,
		func(workitem.Item) costview.Clock { return fixtureClock() }, time.Now(), costview.WithPricing(ratesPricing()))
	near(t, "family total", fam.Total.Derived.USD, 3+3+1.5)
	if got := fam.Total.Derived.Unavailable; len(got) != 1 || got[0] != "model m-ghost has no rate" {
		t.Errorf("unavailable did not propagate: %v", got)
	}
	coder := seatByName(t, fam.Seats, "coder@m-big")
	if coder.FreshInput != 2_000_000 || coder.Output != 100_000 {
		t.Errorf("merged seat tokens = %+v", coder)
	}
	near(t, "merged seat cost", coder.Derived.USD, 7.5)
	if !strings.Contains(costview.FormatDerived(fam.Total.Derived), "unavailable (model m-ghost has no rate)") {
		t.Errorf("total hides the unavailable part: %q", costview.FormatDerived(fam.Total.Derived))
	}
}

// TestDecodeRowCarriesAdapterAndModels pins AC10's costview half: the fields
// the ledger decoder produced arrive on Row, with no parsing in costview.
func TestDecodeRowCarriesAdapterAndModels(t *testing.T) {
	e := rawInvocation(t, "sty_p", time.Now(), map[string]any{"agent": "coder", "adapter": "grok", "model_resolved": "m-big",
		"tokens_in": 10, "model_usage": []map[string]any{
			{"id": "m-big", "tokens_in": 6, "tokens_cache_read": 2}, {"id": "m-small", "tokens_in": 4}}})
	row, ok := costview.DecodeRow(e)
	if !ok || row.Adapter != "grok" || len(row.Models) != 2 || row.Models[0].ID != "m-big" || row.Models[0].TokensCacheRead != 2 {
		t.Errorf("row = %+v", row)
	}
}
