package costview

import (
	"fmt"
	"time"
)

// FormatUSD renders a dollar sum together with how many rows fed it. costed
// is the number of rows that had a known cost_usd; uncosted is the rest.
// costed==0 means the whole set is unpriced — "unavailable", never "$0.00" —
// with the row count inline so it is never mistaken for zero spend.
func FormatUSD(sum float64, costed, uncosted int) string {
	if costed == 0 {
		if uncosted == 0 {
			return "unavailable"
		}
		return fmt.Sprintf("unavailable (%d rows)", uncosted)
	}
	if uncosted == 0 {
		return fmt.Sprintf("$%.2f", sum)
	}
	return fmt.Sprintf("$%.2f (+%d unavailable)", sum, uncosted)
}

// FormatTokens renders a token count, or "unavailable" for a negative
// sentinel — a headline figure never prints a bare 0 when nothing measured it,
// so callers that cannot tell "zero" from "unmeasured" pass -1.
func FormatTokens(n int) string {
	if n < 0 {
		return "unavailable"
	}
	return fmt.Sprintf("%d", n)
}

// FormatTokensFigure renders one of Figures' fresh/output/cache fields via
// FormatTokens, but as "unavailable" when hasRows is false — no dispatch or
// driver row fed the figure at all (pass f.HasRows()), so a literal 0 would
// claim a measured zero rather than nothing having been recorded yet.
func FormatTokensFigure(n int, hasRows bool) string {
	if !hasRows {
		return "unavailable"
	}
	return FormatTokens(n)
}

// FormatTokensMeasured renders a headline token figure keyed on how many rows
// actually measured usage (measuredRows) versus rows that were recorded but
// reported no usage at all (unmeasuredRows). measuredRows==0 never prints a
// literal 0 — "unavailable" alone when nothing was recorded, or "unavailable
// (N unreported)" when rows exist but none measured usage, so a story whose
// gate/driver rows are all unreported (priced or not) can never be mistaken
// for a story that measured a real zero.
func FormatTokensMeasured(n, measuredRows, unmeasuredRows int) string {
	if measuredRows > 0 {
		return FormatTokens(n)
	}
	if unmeasuredRows > 0 {
		return fmt.Sprintf("unavailable (%d unreported)", unmeasuredRows)
	}
	return "unavailable"
}

// FormatSplitTokens renders a fresh-input/cache-read/cache-write headline
// figure — figures that exist only for a row that reported the fresh/cache
// split, not merely "usage available" in general. splitRows counts measured
// rows that reported the split; unsplitRows counts measured rows that
// reported only a legacy cache-inclusive total (pre-split schema, folded into
// UnsplitTokens instead); unmeasuredRows counts rows whose provider reported
// no usage at all. A story whose every measured row is legacy-unsplit
// (splitRows==0 but unsplitRows>0) must read "unavailable (N unsplit)", never
// a literal 0 — that 0 would be the empty sum of a set this figure was never
// measured for, not a genuine zero.
func FormatSplitTokens(n, splitRows, unsplitRows, unmeasuredRows int) string {
	if splitRows > 0 {
		return FormatTokens(n)
	}
	if unsplitRows > 0 {
		return fmt.Sprintf("unavailable (%d unsplit)", unsplitRows)
	}
	if unmeasuredRows > 0 {
		return fmt.Sprintf("unavailable (%d unreported)", unmeasuredRows)
	}
	return "unavailable"
}

// FormatDuration renders a millisecond duration compactly (e.g. 3.1s, 2m4s),
// or "unavailable" for a negative sentinel.
func FormatDuration(ms int64) string {
	if ms < 0 {
		return "unavailable"
	}
	if ms == 0 {
		return "0s"
	}
	return (time.Duration(ms) * time.Millisecond).Round(100 * time.Millisecond).String()
}

// FormatEstimate renders the estimate of unit among estimates beside a
// measured value of the SAME unit — "" when none is recorded, so a caller
// never pads a headline row with an empty parenthetical. The legacy "tokens"
// unit is deliberately never handed to this: it prints only via its own
// cache-inclusive line (see the "estimate-tokens" tag).
func FormatEstimate(estimates []Estimate, unit string) string {
	e, ok := EstimateFor(estimates, unit)
	if !ok {
		return ""
	}
	switch unit {
	case "usd":
		return fmt.Sprintf("est. $%.2f", e.Value)
	case "minutes":
		return fmt.Sprintf("est. %s", FormatDuration(int64(e.Value)*60*1000))
	default:
		return fmt.Sprintf("est. %d", int64(e.Value))
	}
}

// FormatCostPerReject renders a gate-value row's dollars-per-reject: the known
// dollar subtotal divided by rejects, with the uncosted count inline. "n/a"
// when there were no rejects — division by a reject count of zero is not a
// cost of zero.
func FormatCostPerReject(costUSD float64, costed, uncosted, rejects int) string {
	if rejects == 0 {
		return "n/a"
	}
	if costed == 0 {
		if uncosted == 0 {
			return "unavailable"
		}
		return fmt.Sprintf("unavailable (%d uncosted)", uncosted)
	}
	per := costUSD / float64(rejects)
	if uncosted == 0 {
		return fmt.Sprintf("$%.2f", per)
	}
	return fmt.Sprintf("$%.2f (%d uncosted)", per, uncosted)
}

// FormatLegacyTokenEstimate renders the legacy cache-inclusive "tokens"
// estimate on its own line, distinct from the fresh/output headline so it is
// never mistaken for the same quantity.
func FormatLegacyTokenEstimate(estimates []Estimate) string {
	e, ok := EstimateFor(estimates, "tokens")
	if !ok {
		return ""
	}
	return fmt.Sprintf("estimate (legacy, cache-inclusive tokens): %d", int64(e.Value))
}
