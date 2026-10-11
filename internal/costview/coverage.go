package costview

import (
	"fmt"
	"sort"
	"strings"
)

// Driver coverage statuses. A figure that leaves out the driving (in-loop)
// session's own spend is the gated-and-dispatched total, not the whole — the
// status says which it is so a reader can never mistake one for the other.
const (
	DriverMeasured    = "measured"    // every driver row read a usage figure
	DriverDerived     = "derived"     // every driver row has a figure, at least one backfilled from timestamps
	DriverPartial     = "partial"     // some driver rows read one, some did not
	DriverUnavailable = "unavailable" // driver rows exist, none read a usage figure
	DriverNone        = "none"        // no driver row was recorded at all
)

// DriverCoverage is what one adapter's driver_usage rows measured in a set of
// figures. It is derived from the rows' Executable and their adapter-named
// unavailable reasons — never from a per-adapter table — so a new harness shows up
// here the moment its rows do.
type DriverCoverage struct {
	Executable string `json:"executable"`
	Rows       int    `json:"rows"`
	// UsageRows is how many of Rows read a token figure; CostRows how many carried
	// a dollar figure. The reasons name why the rest did not, adapter-named.
	UsageRows int `json:"usage_rows"`
	CostRows  int `json:"cost_rows"`
	// BackfilledRows is how many of UsageRows were backfilled from session-record
	// timestamps rather than read live (BackfilledLabel).
	BackfilledRows int `json:"backfilled_rows,omitempty"`
	// AmbiguousRows is how many of Rows are available but ambiguous (AmbiguousLabel):
	// counted in neither UsageRows nor CostRows, and not a gap either.
	AmbiguousRows      int      `json:"ambiguous_rows,omitempty"`
	UsageUnavailableBy []string `json:"usage_unavailable_by,omitempty"`
	CostUnavailableBy  []string `json:"cost_unavailable_by,omitempty"`
}

// driverStatus classifies a coverage set: none, measured, partial or unavailable.
func driverStatus(cov []DriverCoverage) string {
	rows, usage, backfilled, ambiguous := 0, 0, 0, 0
	for _, c := range cov {
		rows += c.Rows
		usage += c.UsageRows
		backfilled += c.BackfilledRows
		ambiguous += c.AmbiguousRows
	}
	switch {
	case rows == 0:
		return DriverNone
	case ambiguous > 0:
		return DriverPartial // part of the spend is not this story's alone

	case usage == rows && backfilled > 0:
		return DriverDerived
	case usage == rows:
		return DriverMeasured
	case usage == 0:
		return DriverUnavailable
	default:
		return DriverPartial
	}
}

// addCoverage folds one driver row into cov, keyed by Executable.
func addCoverage(cov []DriverCoverage, d DriverRow) []DriverCoverage {
	exe := strings.TrimSpace(d.Executable)
	if exe == "" {
		exe = "unknown"
	}
	idx := -1
	for i := range cov {
		if cov[i].Executable == exe {
			idx = i
			break
		}
	}
	if idx < 0 {
		cov = append(cov, DriverCoverage{Executable: exe})
		idx = len(cov) - 1
	}
	c := &cov[idx]
	c.Rows++
	switch {
	case d.Ambiguous():
		c.AmbiguousRows++
		return cov
	case d.measured():
		c.UsageRows++
		if d.Backfilled {
			c.BackfilledRows++
		}
	default:
		c.UsageUnavailableBy = addReason(c.UsageUnavailableBy, d.unmeasuredReason(), exe)
	}
	if d.CostUSD != nil {
		c.CostRows++
	} else {
		c.CostUnavailableBy = addReason(c.CostUnavailableBy, d.CostUnavailableReason, exe)
	}
	return cov
}

// addReason appends reason to reasons once. A row that carries no reason still
// names its adapter — an unavailable is never anonymous.
func addReason(reasons []string, reason, exe string) []string {
	if strings.TrimSpace(reason) == "" {
		reason = exe + ": no reason recorded"
	}
	for _, r := range reasons {
		if r == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}

// foldCoverage merges b into a, summing per adapter and keeping each distinct
// reason once. The result is sorted by adapter so a family fold is order-stable.
func foldCoverage(a, b []DriverCoverage) []DriverCoverage {
	if len(b) == 0 {
		return a
	}
	out := make([]DriverCoverage, len(a))
	copy(out, a)
	for _, c := range b {
		idx := -1
		for i := range out {
			if out[i].Executable == c.Executable {
				idx = i
				break
			}
		}
		if idx < 0 {
			out = append(out, c)
			continue
		}
		o := &out[idx]
		o.Rows += c.Rows
		o.UsageRows += c.UsageRows
		o.CostRows += c.CostRows
		o.BackfilledRows += c.BackfilledRows
		o.AmbiguousRows += c.AmbiguousRows
		o.UsageUnavailableBy = unionReasons(o.UsageUnavailableBy, c.UsageUnavailableBy)
		o.CostUnavailableBy = unionReasons(o.CostUnavailableBy, c.CostUnavailableBy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Executable < out[j].Executable })
	return out
}

func unionReasons(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, r := range b {
		seen := false
		for _, x := range out {
			if x == r {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, r)
		}
	}
	return out
}

// wholeNote is appended wherever a driver figure is missing: the total is then
// the gated-and-dispatched agents' spend, not the whole story's.
const wholeNote = "total is gated-and-dispatched agents only, not the driving session"

// FormatDriverCoverage renders the one-line driver status of f, e.g.
//
//	driver: measured (claude 4 rows)
//	driver: unavailable — pi: no driver-usage reader (2 rows); total is gated-and-dispatched agents only, not the driving session
//	driver: none recorded; total is gated-and-dispatched agents only, not the driving session
func FormatDriverCoverage(f Figures) string {
	switch f.DriverStatus {
	case DriverMeasured:
		return "driver: measured (" + adapterRows(f.Driver) + ")"
	case DriverDerived:
		return "driver: derived (" + adapterRows(f.Driver) + "; " + backfillNote(f.Driver) + ")"
	case DriverPartial:
		var parts []string
		if why := unavailableWhy(f.Driver); why != "" {
			parts = append(parts, why)
		}
		if note := ambiguousNote(f.Driver); note != "" {
			parts = append(parts, note)
		}
		if note := backfillNote(f.Driver); note != "" {
			parts = append(parts, note)
		}
		return "driver: partial — " + strings.Join(parts, "; ") + "; " + wholeNote
	case DriverUnavailable:
		return "driver: unavailable — " + unavailableWhy(f.Driver) + "; " + wholeNote
	default:
		return "driver: none recorded; " + wholeNote
	}
}

func adapterRows(cov []DriverCoverage) string {
	parts := make([]string, 0, len(cov))
	for _, c := range cov {
		parts = append(parts, fmt.Sprintf("%s %d rows", c.Executable, c.Rows))
	}
	return strings.Join(parts, ", ")
}

// backfillNote says how many measured driver rows were backfilled and that they are
// derived, or "" when none were.
func backfillNote(cov []DriverCoverage) string {
	n := 0
	for _, c := range cov {
		n += c.BackfilledRows
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d backfilled (%s)", n, BackfilledLabel)
}

// ambiguousNote says how many driver rows are ambiguous and why they sit outside the
// exact sums, or "" when none are.
func ambiguousNote(cov []DriverCoverage) string {
	n := 0
	for _, c := range cov {
		n += c.AmbiguousRows
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d ambiguous (%s)", n, AmbiguousLabel)
}

func unavailableWhy(cov []DriverCoverage) string {
	var parts []string
	for _, c := range cov {
		if gap := c.Rows - c.UsageRows - c.AmbiguousRows; gap > 0 {
			parts = append(parts, fmt.Sprintf("%s (%d of %d rows)", strings.Join(c.UsageUnavailableBy, "; "), gap, c.Rows))
		}
	}
	return strings.Join(parts, "; ")
}

// FormatAdapterCoverage renders one line per adapter that drove f's stories:
// what its driver rows measured and what they could not, for usage and for cost.
func FormatAdapterCoverage(f Figures) []string {
	out := make([]string, 0, len(f.Driver))
	for _, c := range f.Driver {
		out = append(out, fmt.Sprintf("%s: driver usage %s; driver cost %s",
			c.Executable,
			usageWord(c),
			exactWord(c.CostRows, c, c.CostUnavailableBy)))
	}
	return out
}

// FormatAdapterUsageCoverage is FormatAdapterCoverage without the dollar-cost
// clause: one line per adapter naming what its driver rows measured and could
// not measure for usage. The web page renders it, as it shows no dollar figures.
func FormatAdapterUsageCoverage(f Figures) []string {
	out := make([]string, 0, len(f.Driver))
	for _, c := range f.Driver {
		out = append(out, fmt.Sprintf("%s: driver usage %s", c.Executable, usageWord(c)))
	}
	return out
}

// usageWord is c's usage coverage, noting any backfilled rows it counts as measured.
func usageWord(c DriverCoverage) string {
	w := exactWord(c.UsageRows, c, c.UsageUnavailableBy)
	if note := backfillNote([]DriverCoverage{c}); note != "" {
		w += " — " + note
	}
	if note := ambiguousNote([]DriverCoverage{c}); note != "" {
		w += " — " + note
	}
	return w
}

// exactWord is coverageWord over c's rows that are not ambiguous: an ambiguous row
// is neither measured nor a gap, so it is left out of both sides.
func exactWord(measured int, c DriverCoverage, reasons []string) string {
	rows := c.Rows - c.AmbiguousRows
	if rows == 0 {
		return "none exact"
	}
	return coverageWord(measured, rows, reasons)
}

func coverageWord(measured, rows int, reasons []string) string {
	switch {
	case measured == rows:
		return fmt.Sprintf("measured (%d of %d rows)", measured, rows)
	case measured == 0:
		return fmt.Sprintf("unavailable (%s)", strings.Join(reasons, "; "))
	default:
		return fmt.Sprintf("partial (%d of %d rows; unavailable: %s)", measured, rows, strings.Join(reasons, "; "))
	}
}
