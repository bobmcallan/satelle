package throughput

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/costview"
)

// Text writes rep as the text `satelle stats` prints.
func Text(w io.Writer, rep Report) {
	fmt.Fprintf(w, "window %s to %s (UTC)\n", rep.Window.Since.UTC().Format(time.RFC3339), rep.Window.Until.UTC().Format(time.RFC3339))
	for _, r := range rep.Repos {
		fmt.Fprintf(w, "\n== repo %s (%s) ==\n", r.Name, r.Path)
		section(w, r)
	}
	head := "\n== total ==\n"
	if len(rep.Total.Excluded) > 0 {
		head = fmt.Sprintf("\n== total (excluded, unreadable: %s) ==\n", strings.Join(rep.Total.Excluded, ", "))
	}
	fmt.Fprint(w, head)
	section(w, rep.Total)
}

func section(w io.Writer, r RepoReport) {
	if r.Error != "" {
		fmt.Fprintf(w, "error: %s\n", r.Error)
	}
	fmt.Fprintf(w, "finished: stories %s  epics %s\n", CountText(r.Stories), CountText(r.Epics))
	fmt.Fprintln(w, "per day:")
	buckets(w, r.Days)
	fmt.Fprintln(w, "per week:")
	buckets(w, r.Weeks)
	phase(w, "phase 1 (start to PR step, or to terminal)", r.Phase1)
	phase(w, "phase 2 (PR step to terminal)", r.Phase2)
	fmt.Fprintln(w, "median time in step:")
	if r.Steps.Reason != "" {
		fmt.Fprintf(w, "  unavailable (%s)\n", r.Steps.Reason)
	}
	for _, s := range r.Steps.Rows {
		fmt.Fprintf(w, "  %-16s %s  (%d items)\n", s.Status, DurationText(s.Median), s.Items)
	}
	qualityText(w, r.Quality)
	costSection(w, r.Cost)
	fmt.Fprintln(w, "estimate spread (actual ÷ estimate, nearest rank):")
	for _, s := range r.Spread {
		fmt.Fprintf(w, "  %-11s  items %s  unavailable %s  median %s  p90 %s  min %s  max %s\n", s.Unit,
			CountText(s.Stories), CountText(s.Unavailable), RatioText(s.Median, "%.2f"), RatioText(s.P90, "%.2f"), RatioText(s.Min, "%.2f"), RatioText(s.Max, "%.2f"))
	}
}

func costSection(w io.Writer, c CostSection) {
	fmt.Fprintf(w, "cost (each finished item's own rows): %s\n", UsageText(c.Usage))
	breakdown(w, "  ", c.BySeat, c.ByModel)
	for _, s := range c.Stories {
		fmt.Fprintf(w, "  item %s %q", s.ID, s.Title)
		if s.Repo != "" {
			fmt.Fprintf(w, " (repo %s)", s.Repo)
		}
		fmt.Fprintf(w, ": %s\n", UsageText(s.Usage))
		breakdown(w, "    ", s.BySeat, s.ByModel)
		if s.EstimatesReason != "" {
			fmt.Fprintf(w, "    estimate: unavailable (%s)\n", s.EstimatesReason)
		}
		for _, e := range s.Estimates {
			fmt.Fprintf(w, "    estimate %-11s  est %s  actual %s  actual÷estimate %s\n", e.Unit,
				strconv.FormatFloat(e.Estimate, 'f', -1, 64), actualText(e), RatioText(Ratio{Value: e.Ratio, Reason: e.Reason}, "%.2f"))
		}
	}
}

func breakdown(w io.Writer, indent string, bySeat, byModel []Breakdown) {
	for _, b := range bySeat {
		fmt.Fprintf(w, "%sby seat %-12s %s\n", indent, b.Name, UsageText(b.Usage))
	}
	for _, b := range byModel {
		fmt.Fprintf(w, "%sby model %-11s %s\n", indent, b.Name, UsageText(b.Usage))
	}
}

func actualText(e EstimateVsActual) string {
	if e.Actual == nil {
		return fmt.Sprintf("unavailable (%s)", e.Reason)
	}
	return strconv.Itoa(*e.Actual)
}

// UsageText is how a Usage reads in text: each token class and the cost, a
// figure that was not measured or priced reading unavailable and why.
func UsageText(u Usage) string {
	s := fmt.Sprintf("fresh input %s  cache read %s  cache write %s  output %s",
		CountText(u.FreshInput), CountText(u.CacheRead), CountText(u.CacheWrite), CountText(u.Output))
	if u.UnsplitInput != nil {
		s += "  unsplit input " + CountText(*u.UnsplitInput)
	}
	return s + "  cost " + CostText(u.Cost)
}

// CostText is how a Cost reads in text: costview's derived-cost rendering, or
// unavailable and why.
func CostText(c Cost) string {
	if c.USD == nil {
		return fmt.Sprintf("unavailable (%s)", c.Reason)
	}
	return costview.FormatDerived(c.Derived)
}

func buckets(w io.Writer, bs []Bucket) {
	for _, b := range bs {
		fmt.Fprintf(w, "  %-10s  stories %s  epics %s\n", b.Key, CountText(b.Stories), CountText(b.Epics))
	}
}

func phase(w io.Writer, label string, p Phase) {
	fmt.Fprintf(w, "%s: median %s  p90 %s  (%d items)\n", label, DurationText(p.Median), DurationText(p.P90), p.Items)
}

// CountText is how a Count reads in text: the number, or unavailable and why.
func CountText(c Count) string {
	if c.Value == nil {
		return fmt.Sprintf("unavailable (%s)", c.Reason)
	}
	return fmt.Sprint(*c.Value)
}

// DurationText is how a Duration reads in text: whole seconds, n/a, or
// unavailable and why.
func DurationText(d Duration) string {
	switch {
	case d.NotApplicable:
		return "n/a"
	case d.Seconds == nil:
		return fmt.Sprintf("unavailable (%s)", d.Reason)
	}
	return time.Duration(*d.Seconds * float64(time.Second)).Round(time.Second).String()
}
