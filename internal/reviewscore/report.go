package reviewscore

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

// WriteJSON writes the report to path as indented JSON.
func (r Report) WriteJSON(path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ReadReport loads a report written by WriteJSON.
func ReadReport(path string) (Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Report{}, fmt.Errorf("read report %s: %w", path, err)
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return Report{}, fmt.Errorf("decode report %s: %w", path, err)
	}
	if r.CaseDigest == "" {
		return Report{}, fmt.Errorf("report %s has no case_digest: it was not written by `satelle review score`", path)
	}
	return r, nil
}

// Render prints the report as a table: one line per rubric and a total, one
// column per metric.
func (r Report) Render(w io.Writer) {
	model := strings.Join(r.ModelResolved, ", ")
	if model == "" {
		model = "unavailable (no model reported)"
	}
	fmt.Fprintf(w, "binding %s · adapter %s · model %s · %d case-run(s) over %d case(s) · digest %.12s\n",
		r.Binding, orUnknown(r.Adapter), model, r.Total.Rows, len(r.CaseIDs), r.CaseDigest)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "RUBRIC\t%s\n", strings.ToUpper(strings.Join(MetricNames, "\t")))
	line := func(name string, m Metrics) {
		cells := make([]string, len(MetricNames))
		for i, n := range MetricNames {
			cells[i] = m.Cell(n)
		}
		fmt.Fprintf(tw, "%s\t%s\n", name, strings.Join(cells, "\t"))
	}
	for _, rm := range r.ByRubric {
		line(rm.Rubric, rm.Metrics)
	}
	line("TOTAL", r.Total)
	_ = tw.Flush()
	var errs int
	for _, c := range r.Cases {
		if c.Error != "" {
			errs++
		}
	}
	if r.Total.Unfaithful > 0 {
		fmt.Fprintf(w, "%d unfaithful replay case-run(s) were not judged and are in no figure above (the replay lacks material the recorded reviewer judged):\n", r.Total.Unfaithful)
		for _, c := range r.Cases {
			if c.Outcome == OutcomeUnfaithful {
				fmt.Fprintf(w, "  %s/%s: %s\n", c.Rubric, c.ID, strings.Join(c.PayloadGaps, "; "))
			}
		}
	}
	if errs > 0 {
		fmt.Fprintf(w, "%d case-run(s) produced no verdict because the judge failed; see the \"error\" field in the JSON report\n", errs)
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
