package costview

import (
	"strconv"
	"strings"
)

// Estimate is one legacy plan estimate figure, kept in the unit it was
// written in (sty_8eae81ac's estimate-* tags). Unit is one of "usd",
// "fresh-input", "output", "minutes", or the legacy cache-inclusive "tokens" —
// never converted into another unit or conflated with a measured figure.
type Estimate struct {
	Unit  string  `json:"unit"`
	Value float64 `json:"value"`
}

// estimateUnits are the tag suffixes ParseEstimates recognises, in the order
// they are checked — storyEstimate (verb/workitem.go) is the sole writer.
var estimateUnits = []string{"usd", "fresh-input", "output", "minutes", "tokens"}

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
