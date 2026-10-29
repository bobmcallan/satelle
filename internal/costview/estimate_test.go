package costview_test

import (
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/costview"
)

// Rounding is pinned rather than accidental: whole minutes truncate, but any
// positive duration under a minute records as 1 rather than 0 (sty_ef8a896b).
func TestWholeMinutes(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want int
	}{
		{38 * time.Minute, 38},
		{2 * time.Hour, 120},
		{90 * time.Second, 1},
		{45 * time.Second, 1},
		{time.Nanosecond, 1},
		{0, 0},
	}
	for _, c := range cases {
		if got := costview.WholeMinutes(c.in); got != c.want {
			t.Errorf("WholeMinutes(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// ActualFor pairs each estimate unit with its measured figure, and says when the
// figure was not measured rather than reading it as zero.
func TestActualForEachEstimateUnit(t *testing.T) {
	measured := costview.Figures{
		FreshInput: 100, UnsplitTokens: 7, Output: 20, CacheRead: 1000, CacheWrite: 3,
		UsageRows: 2, UnsplitRows: 1, ElapsedMs: (2*time.Minute + 30*time.Second).Milliseconds(),
	}
	want := map[string]int{
		costview.UnitFreshInput: 100,
		costview.UnitOutput:     20,
		costview.UnitTokens:     100 + 7 + 20 + 3, // cache read excluded
		costview.UnitMinutes:    2,
	}
	for unit, w := range want {
		if got, ok := costview.ActualFor(unit, measured); !ok || got != w {
			t.Errorf("ActualFor(%s) = %d, %v; want %d, true", unit, got, ok, w)
		}
	}

	none := costview.Figures{UsageUnavailableRows: 1, ElapsedMs: -1}
	for _, unit := range costview.EstimateUnits() {
		if _, ok := costview.ActualFor(unit, none); ok {
			t.Errorf("ActualFor(%s) of an unmeasured story reported a figure", unit)
		}
	}
	unsplit := costview.Figures{UnsplitTokens: 50, UsageRows: 1, UnsplitRows: 1}
	if _, ok := costview.ActualFor(costview.UnitFreshInput, unsplit); ok {
		t.Error("fresh input of an all-unsplit story must be unmeasured")
	}
	if _, ok := costview.ActualFor("usd", measured); ok {
		t.Error("usd is not an estimate unit")
	}
}
