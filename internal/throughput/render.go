package throughput

import (
	"fmt"
	"io"
	"strings"
	"time"
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
