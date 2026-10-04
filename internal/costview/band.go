package costview

// CostBand is the item's cost band: one of low, medium, high — computed
// ONLY from measured actual tokens (fresh input + unsplit input + output +
// cache read + cache write). Dollars, elapsed time and provider figures are
// never inputs. Returns "" when no row measured usage (nothing to band).
//
// Boundaries on total actual tokens:
//
//	low:    < 200000
//	medium: 200000 <= t < 2000000
//	high:   >= 2000000
//
// The token sum deliberately includes cache read, unlike the actual-tokens
// tag (which excludes it: reused context is not new work). Cache read IS a
// band input per the story body — do not unify the two expressions.
func (f Figures) CostBand() string {
	if !f.HasRows() {
		return "" // nothing measured -> no band
	}
	total := f.FreshInput + f.UnsplitTokens + f.Output + f.CacheRead + f.CacheWrite
	switch {
	case total < 200000:
		return "low"
	case total < 2000000:
		return "medium"
	default:
		return "high"
	}
}

// FormatCostBand renders CostBand for display. An empty band (nothing
// measured) is the word "unavailable", never a false "low". That word lives
// here, not in CostBand, so a recorder can tell "no band" from a real band
// and must not write an actual-cost tag when CostBand is empty. printCostSummary
// and the web cost view both call this, the way they both call FormatUSD.
func FormatCostBand(f Figures) string {
	if band := f.CostBand(); band != "" {
		return band
	}
	return "unavailable"
}
