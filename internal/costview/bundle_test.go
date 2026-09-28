package costview

import (
	"math"
	"testing"
)

// sty_23e10d92: a bundled reviewer session is ONE measured call; a per-rubric
// figure is an allocation of it. The shares of one row must sum to the measured
// figure exactly — an allocation that drifts is a second, invented measurement.

func TestBundleShareSumsExactlyToTheMeasuredRow(t *testing.T) {
	cost := 0.1 // not divisible by 3 in binary floating point
	row := Row{
		TokensIn: 1000, TokensOut: 101, TokensTotal: 1101, TokensInFresh: 7, TokensCacheWrite: 500, TokensCacheRead: 493,
		DurationMs: 1000, CostUSD: &cost, UsageAvailable: true,
	}
	const n = 3
	var in, out, total, fresh, write, read int
	var ms int64
	var sum float64
	for i := 0; i < n; i++ {
		s := bundleShare(row, i, n)
		in, out, total = in+s.TokensIn, out+s.TokensOut, total+s.TokensTotal
		fresh, write, read = fresh+s.TokensInFresh, write+s.TokensCacheWrite, read+s.TokensCacheRead
		ms += s.DurationMs
		sum += *s.CostUSD
	}
	if in != 1000 || out != 101 || total != 1101 || fresh != 7 || write != 500 || read != 493 || ms != 1000 {
		t.Errorf("token shares must sum to the measured row: in=%d out=%d total=%d fresh=%d write=%d read=%d ms=%d",
			in, out, total, fresh, write, read, ms)
	}
	if math.Abs(sum-cost) > 1e-12 {
		t.Errorf("cost shares sum to %v, want %v", sum, cost)
	}
}

func TestBundleShareKeepsUnavailableCostUnavailable(t *testing.T) {
	row := Row{TokensIn: 10, UsageAvailable: false}
	for i := 0; i < 2; i++ {
		s := bundleShare(row, i, 2)
		if s.CostUSD != nil || s.UsageAvailable {
			t.Errorf("share %d of an unavailable row must stay unavailable: %+v", i, s)
		}
	}
}

func TestAllocationNote(t *testing.T) {
	if got := allocationNote(nil); got != "" {
		t.Errorf("no bundle, no note: %q", got)
	}
	if got := allocationNote([]string{"bun_1"}); got != "allocated share of bundle bun_1" {
		t.Errorf("one bundle: %q", got)
	}
	if got := allocationNote([]string{"bun_1", "bun_2"}); got != "allocated share of 2 bundles (bun_1, bun_2)" {
		t.Errorf("two bundles: %q", got)
	}
}
