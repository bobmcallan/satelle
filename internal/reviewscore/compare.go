package reviewscore

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// CompareRow is one rubric × metric line of a comparison.
type CompareRow struct {
	Rubric string
	Metric string
	A, B   string
	Delta  string
}

// Comparison is two reports of the same case set, side by side.
type Comparison struct {
	A, B Report
	Rows []CompareRow
}

// Compare lines two reports up per rubric and in total. It refuses when they
// were not scored over the same case set (or the same number of runs), because
// every figure is then a figure about different work.
func Compare(a, b Report) (Comparison, error) {
	if a.CaseDigest != b.CaseDigest {
		onlyA, onlyB := setDiff(a.CaseIDs, b.CaseIDs)
		msg := fmt.Sprintf("the reports were not scored over the same case set: %s digest %.12s, %s digest %.12s",
			a.Binding, a.CaseDigest, b.Binding, b.CaseDigest)
		if len(onlyA) > 0 {
			msg += fmt.Sprintf("; only in %s: %s", a.Binding, strings.Join(onlyA, ", "))
		}
		if len(onlyB) > 0 {
			msg += fmt.Sprintf("; only in %s: %s", b.Binding, strings.Join(onlyB, ", "))
		}
		if len(onlyA) == 0 && len(onlyB) == 0 {
			msg += "; the same case ids carry different content"
		}
		return Comparison{}, fmt.Errorf("%s", msg)
	}
	if a.Runs != b.Runs {
		return Comparison{}, fmt.Errorf("the reports were scored with different run counts (%s ran each case %d time(s), %s %d): totals are not comparable",
			a.Binding, a.Runs, b.Binding, b.Runs)
	}
	c := Comparison{A: a, B: b}
	bm := map[string]Metrics{}
	for _, rm := range b.ByRubric {
		bm[rm.Rubric] = rm.Metrics
	}
	add := func(rubric string, ma, mb Metrics) {
		for _, n := range MetricNames {
			c.Rows = append(c.Rows, CompareRow{Rubric: rubric, Metric: n, A: ma.Cell(n), B: mb.Cell(n), Delta: delta(n, ma, mb)})
		}
	}
	for _, rm := range a.ByRubric {
		add(rm.Rubric, rm.Metrics, bm[rm.Rubric])
	}
	add("TOTAL", a.Total, b.Total)
	return c, nil
}

// Render prints the comparison with one column per binding.
func (c Comparison) Render(w io.Writer) {
	fmt.Fprintf(w, "%s vs %s · same case set (digest %.12s) · %d run(s) per case\n", c.A.Binding, c.B.Binding, c.A.CaseDigest, c.A.Runs)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "RUBRIC\tMETRIC\t%s\t%s\tDELTA (B-A)\n", strings.ToUpper(c.A.Binding), strings.ToUpper(c.B.Binding))
	last := ""
	for _, r := range c.Rows {
		name := r.Rubric
		if name == last {
			name = ""
		}
		last = r.Rubric
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", name, r.Metric, r.A, r.B, r.Delta)
	}
	_ = tw.Flush()
}

// delta is B minus A where both sides measured the figure, "n/a" otherwise.
func delta(metric string, a, b Metrics) string {
	switch metric {
	case "recall":
		if a.Defect == 0 || b.Defect == 0 {
			return "n/a"
		}
		return fmt.Sprintf("%+.1fpp", 100*(float64(b.Caught)/float64(b.Defect)-float64(a.Caught)/float64(a.Defect)))
	case "false blockers":
		return signed(b.FalseBlockers - a.FalseBlockers)
	case "escapes":
		return signed(b.Escapes - a.Escapes)
	case "no verdict":
		return signed(b.NoVerdict - a.NoVerdict)
	case "finding match":
		if a.FindingScored == 0 || b.FindingScored == 0 {
			return "n/a"
		}
		return signed(b.FindingMatched - a.FindingMatched)
	case "wall time":
		return (time.Duration(b.WallMs-a.WallMs) * time.Millisecond).Round(time.Millisecond).String()
	case "tokens":
		if a.UsageRuns != a.Rows || b.UsageRuns != b.Rows || a.Rows == 0 {
			return "n/a"
		}
		return signed((b.TokensIn + b.TokensOut) - (a.TokensIn + a.TokensOut))
	case "cost":
		if a.CostRuns != a.Rows || b.CostRuns != b.Rows || a.Rows == 0 {
			return "n/a"
		}
		return fmt.Sprintf("%+.4f", b.CostUSD-a.CostUSD)
	}
	return ""
}

func signed(n int) string { return fmt.Sprintf("%+d", n) }

// setDiff returns the members only in a and only in b, sorted.
func setDiff(a, b []string) (onlyA, onlyB []string) {
	in := func(xs []string) map[string]bool {
		m := map[string]bool{}
		for _, x := range xs {
			m[x] = true
		}
		return m
	}
	ma, mb := in(a), in(b)
	for x := range ma {
		if !mb[x] {
			onlyA = append(onlyA, x)
		}
	}
	for x := range mb {
		if !ma[x] {
			onlyB = append(onlyB, x)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	return onlyA, onlyB
}
