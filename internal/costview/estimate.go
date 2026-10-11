package costview

import (
	"strconv"
	"strings"
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

// estimateUnits are the tag suffixes ParseEstimates recognises, in the order
// they are checked — storyEstimate (verb/workitem.go) is the sole writer.
// "usd" is deliberately absent: a dollar tag is not an estimate.
var estimateUnits = []string{"fresh-input", "output", "minutes", "tokens"}

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
