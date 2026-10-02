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
	BackfilledRows     int      `json:"backfilled_rows,omitempty"`
	UsageUnavailableBy []string `json:"usage_unavailable_by,omitempty"`
	CostUnavailableBy  []string `json:"cost_unavailable_by,omitempty"`
}

// driverStatus classifies a coverage set: none, measured, partial or unavailable.
func driverStatus(cov []DriverCoverage) string {
	rows, usage, backfilled := 0, 0, 0
	for _, c := range cov {
		rows += c.Rows
		usage += c.UsageRows
		backfilled += c.BackfilledRows
	}
	switch {
	case rows == 0:
		return DriverNone
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
	if d.measured() {
		c.UsageRows++
		if d.Backfilled {
			c.BackfilledRows++
		}
	} else {
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
		line := "driver: partial — " + unavailableWhy(f.Driver)
		if note := backfillNote(f.Driver); note != "" {
			line += "; " + note
		}
		return line + "; " + wholeNote
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

func unavailableWhy(cov []DriverCoverage) string {
	var parts []string
	for _, c := range cov {
		if c.UsageRows < c.Rows {
			parts = append(parts, fmt.Sprintf("%s (%d of %d rows)", strings.Join(c.UsageUnavailableBy, "; "), c.Rows-c.UsageRows, c.Rows))
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
			coverageWord(c.CostRows, c.Rows, c.CostUnavailableBy)))
	}
	return out
}

// usageWord is c's usage coverage, noting any backfilled rows it counts as measured.
func usageWord(c DriverCoverage) string {
	w := coverageWord(c.UsageRows, c.Rows, c.UsageUnavailableBy)
	if note := backfillNote([]DriverCoverage{c}); note != "" {
		w += " — " + note
	}
	return w
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
