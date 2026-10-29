package costview

import (
	"strconv"
	"strings"
	"time"
)

// Estimate is one plan estimate figure, kept in the unit it was written in.
// Unit is one of "fresh-input", "output", "minutes", or the legacy
// cache-inclusive "tokens" — never converted into another unit or conflated
// with a measured figure. A stored estimate-usd tag is not a unit this parser
// reads: it is left as stored and ignored by the display and the estimate gate.
type Estimate struct {
	Unit  string  `json:"unit"`
	Value float64 `json:"value"`
}

// The units an estimate-<unit> tag is written in.
const (
	UnitFreshInput = "fresh-input"
	UnitOutput     = "output"
	UnitMinutes    = "minutes"
	UnitTokens     = "tokens"
)

// estimateUnits are the tag suffixes ParseEstimates recognises, in the order
// they are checked — storyEstimate (verb/workitem.go) is the sole writer.
// "usd" is deliberately absent: a dollar tag is not an estimate.
var estimateUnits = []string{UnitFreshInput, UnitOutput, UnitMinutes, UnitTokens}

// EstimateUnits is every unit an estimate may be written in, in display order.
func EstimateUnits() []string { return append([]string(nil), estimateUnits...) }

// ActualFor is f's actual in an estimate unit — the figure an estimate of that
// unit is judged against — and whether f measured it. Tokens is the summary
// figure: fresh input + unsplit input + output + cache write, cache read
// excluded — reused context is not new work (sty_8eae81ac) — with unsplit input
// folded in so a legacy row with no fresh/cache split never vanishes from it.
// Minutes is the story clock in WholeMinutes. Fresh input is unmeasured when
// every input row was unsplit.
func ActualFor(unit string, f Figures) (int, bool) {
	switch unit {
	case UnitFreshInput:
		return f.FreshInput, f.HasRows() && !(f.FreshInput == 0 && f.UnsplitTokens > 0)
	case UnitOutput:
		return f.Output, f.HasRows()
	case UnitTokens:
		return f.FreshInput + f.UnsplitTokens + f.Output + f.CacheWrite, f.HasRows()
	case UnitMinutes:
		// A negative ElapsedMs is the never-engaged sentinel, not a span.
		if f.ElapsedMs < 0 {
			return 0, false
		}
		return WholeMinutes(time.Duration(f.ElapsedMs) * time.Millisecond), true
	}
	return 0, false
}

// WholeMinutes is d in the whole minutes a minutes tag stores. Truncating alone
// would record real work as "0 minutes" for anything under a minute, so any
// positive duration is worth at least 1 (sty_ef8a896b).
func WholeMinutes(d time.Duration) int {
	m := int(d.Minutes())
	if m == 0 && d > 0 {
		m = 1
	}
	return m
}

// ParseEstimates reads estimate-<unit>:<value> tags off tags and returns each
// as its own Estimate, in its own unit. A tag whose value does not parse as a
// number is skipped rather than guessed at.
func ParseEstimates(tags []string) []Estimate {
	var out []Estimate
	for _, unit := range estimateUnits {
		prefix := "estimate-" + unit + ":"
		for _, t := range tags {
			v, ok := strings.CutPrefix(t, prefix)
			if !ok {
				continue
			}
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				continue
			}
			out = append(out, Estimate{Unit: unit, Value: f})
		}
	}
	return out
}

// EstimateFor returns the first Estimate of unit among estimates, and whether
// one was found.
func EstimateFor(estimates []Estimate, unit string) (Estimate, bool) {
	for _, e := range estimates {
		if e.Unit == unit {
			return e, true
		}
	}
	return Estimate{}, false
}

// Actual is one measured actual figure read off a finished story's tags, in
// the unit recordActual wrote it in. Unit is one of "fresh-input", "output",
// "cache-read" or "cache-write".
type Actual struct {
	Unit  string  `json:"unit"`
	Value float64 `json:"value"`
}

// actualUnits are the actual-<unit> tags ParseActuals reads — the token
// figures recordActual (verb/workitem.go) is the sole writer of. A dollar unit
// is deliberately absent, as it is for estimates.
var actualUnits = []string{"fresh-input", "output", "cache-read", "cache-write"}

// ParseActuals reads actual-<unit>:<value> tags off tags. A value that does not
// parse as a number — notably recordActual's "unavailable" marker — is skipped,
// so an unmeasured figure is absent rather than a false zero.
func ParseActuals(tags []string) []Actual {
	var out []Actual
	for _, unit := range actualUnits {
		prefix := "actual-" + unit + ":"
		for _, t := range tags {
			v, ok := strings.CutPrefix(t, prefix)
			if !ok {
				continue
			}
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				continue
			}
			out = append(out, Actual{Unit: unit, Value: f})
			break
		}
	}
	return out
}
