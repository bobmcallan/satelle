package costview

import "testing"

// TestCostBandBoundaries pins each side of both boundaries, a measured zero,
// and the unmeasured result. The numbers are the table next to CostBand, not
// a shared constant the function and the test could drift together.
func TestCostBandBoundaries(t *testing.T) {
	cases := []struct {
		name string
		f    Figures
		want string
	}{
		{"unmeasured", Figures{}, ""},
		{"measured zero", Figures{UsageRows: 1}, "low"},
		{"just below low/medium", Figures{UsageRows: 1, FreshInput: 199_999}, "low"},
		{"low/medium boundary", Figures{UsageRows: 1, FreshInput: 200_000}, "medium"},
		{"just above low/medium", Figures{UsageRows: 1, FreshInput: 200_001}, "medium"},
		{"just below medium/high", Figures{UsageRows: 1, FreshInput: 1_999_999}, "medium"},
		{"medium/high boundary", Figures{UsageRows: 1, Output: 2_000_000}, "high"},
		{"just above medium/high", Figures{UsageRows: 1, CacheRead: 2_000_001}, "high"},
		// Cache read and unsplit input are band inputs. 100+50+20+200_000+5
		// crosses 200000 only because cache read is in the sum.
		{"composed sum includes cache read and unsplit", Figures{
			UsageRows: 1, FreshInput: 100, UnsplitTokens: 50, Output: 20, CacheRead: 200_000, CacheWrite: 5,
		}, "medium"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.f.CostBand(); got != c.want {
				t.Fatalf("CostBand() = %q, want %q (figures %+v)", got, c.want, c.f)
			}
		})
	}
}

// TestCostBandIgnoresDollarsAndTime pins that a provider dollar figure and
// elapsed time are not inputs: the same token counts band the same when
// CostUSD or ElapsedMs change, and an unmeasured row stays unbanded even
// when it is priced.
func TestCostBandIgnoresDollarsAndTime(t *testing.T) {
	base := Figures{UsageRows: 2, FreshInput: 1000}
	priced := base
	priced.CostUSD = 1e6
	priced.ElapsedMs = 9_000_000_000
	if base.CostBand() != "low" || priced.CostBand() != base.CostBand() {
		t.Fatalf("band changed with dollars/time: base %q priced %q", base.CostBand(), priced.CostBand())
	}
	unmeasured := Figures{CostUSD: 1e6, ElapsedMs: 1000}
	if got := unmeasured.CostBand(); got != "" {
		t.Fatalf("unmeasured with dollars/time = %q, want empty", got)
	}
}

// TestCostBandOnFoldedTotalIsNotASumOfBands pins the epic rule: two low
// figures fold into one token total, and that total is banded by the same
// table. The result is not "low"+"low" and not a sum of dollar costs.
func TestCostBandOnFoldedTotalIsNotASumOfBands(t *testing.T) {
	a := Figures{UsageRows: 1, FreshInput: 150_000, CostUSD: 1}
	b := Figures{UsageRows: 1, FreshInput: 150_000, CostUSD: 50}
	if a.CostBand() != "low" || b.CostBand() != "low" {
		t.Fatalf("each side should be low, got %q and %q", a.CostBand(), b.CostBand())
	}
	total := Fold(a, b)
	if total.CostBand() != "medium" {
		t.Fatalf("folded total band = %q, want medium (300000 tokens, not a sum of bands)", total.CostBand())
	}
	if total.CostUSD != 51 {
		t.Fatalf("fold still sums dollars for other readers, got %v", total.CostUSD)
	}
}

// TestFormatCostBandMapsEmptyToUnavailable pins the display word: CostBand
// itself never returns "unavailable", and the shared helper both surfaces
// call does.
func TestFormatCostBandMapsEmptyToUnavailable(t *testing.T) {
	if got := (Figures{}).CostBand(); got != "" {
		t.Fatalf("CostBand unmeasured = %q, want empty (not the display word)", got)
	}
	if got := FormatCostBand(Figures{}); got != "unavailable" {
		t.Fatalf("FormatCostBand unmeasured = %q, want unavailable", got)
	}
	measured := Figures{UsageRows: 1, FreshInput: 10}
	if got := FormatCostBand(measured); got != "low" {
		t.Fatalf("FormatCostBand measured = %q, want low", got)
	}
}
