package costview

// DriverRowView is one driver_usage row (or the TOTAL fold across every row
// for a story) pre-formatted through the SAME formatters the headline
// Figures use. It is the single formatting pass FormatDriverRows produces —
// the CLI's DRIVER SESSION table and the web page's Driver sessions table
// both render THIS, so identical ledger data can never come out as two
// different strings on the two surfaces (sty_b8542a3a AC7 rework).
type DriverRowView struct {
	SessionID, Executable, Trigger string
	FreshIn, Out                   string
	CacheRead, CacheWrite          string
	USD                            string
	AgentTime                      string
}

// FormatDriverRows renders rows into per-session DriverRowView lines plus a
// TOTAL fold (nil when rows is empty). An unavailable row's fresh/output/
// cache figures render as "—", never a folded-in 0. The TOTAL row's fresh/
// output/cache columns use FormatTokensFigure keyed on whether ANY row
// measured usage at all: when every row is unavailable, the sum is 0 by
// construction but that 0 would misrepresent "nothing measured" as "measured
// zero" — the fix for the AC1 regression where the TOTAL column printed
// "0 (measured; N of N unreported)", showing unavailable as a literal zero.
func FormatDriverRows(rows []DriverRow) ([]DriverRowView, *DriverRowView) {
	if len(rows) == 0 {
		return nil, nil
	}
	var out []DriverRowView
	var totalFresh, totalOut, totalCacheRead, totalCacheWrite, measured int
	var totalUSD float64
	var costed, uncosted int
	var totalWallMs int64
	for _, d := range rows {
		freshIn, outCol, cacheRead, cacheWrite := "—", "—", "—", "—"
		if d.Available {
			freshIn, outCol = FormatTokens(d.FreshInput), FormatTokens(d.Output)
			cacheRead, cacheWrite = FormatTokens(d.CacheRead), FormatTokens(d.CacheWrite)
			totalFresh += d.FreshInput
			totalOut += d.Output
			totalCacheRead += d.CacheRead
			totalCacheWrite += d.CacheWrite
			measured++
		}
		usd := FormatUSD(0, 0, 1)
		if d.CostUSD != nil {
			usd = FormatUSD(*d.CostUSD, 1, 0)
			totalUSD += *d.CostUSD
			costed++
		} else {
			uncosted++
		}
		wallMs := int64(d.WallSeconds * 1000)
		totalWallMs += wallMs
		out = append(out, DriverRowView{
			SessionID: d.SessionID, Executable: d.Executable, Trigger: d.Trigger,
			FreshIn: freshIn, Out: outCol, CacheRead: cacheRead, CacheWrite: cacheWrite,
			USD: usd, AgentTime: FormatDuration(wallMs),
		})
	}
	total := DriverRowView{
		SessionID:  "TOTAL",
		FreshIn:    FormatTokensFigure(totalFresh, measured > 0),
		Out:        FormatTokensFigure(totalOut, measured > 0),
		CacheRead:  FormatTokensFigure(totalCacheRead, measured > 0),
		CacheWrite: FormatTokensFigure(totalCacheWrite, measured > 0),
		USD:        FormatUSD(totalUSD, costed, uncosted),
		AgentTime:  FormatDuration(totalWallMs),
	}
	return out, &total
}
