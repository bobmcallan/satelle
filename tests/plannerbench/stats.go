//go:build plannerbench

package plannerbench

import "sort"

// metricSummary is one metric over one cell's samples. Available is false when
// no sample reported the metric — distinct from a reported zero, the same
// distinction usageEvidence keeps.
type metricSummary struct {
	Name      string   `json:"name"`
	Available bool     `json:"available"`
	N         int      `json:"n"`
	P50       *float64 `json:"p50,omitempty"`
	P90       *float64 `json:"p90,omitempty"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
}

// percentile returns the p-th percentile (0..100) using the NEAREST-RANK rule:
// sort ascending, take the value at ceil(p/100 * n), clamped to the last index.
// No interpolation — with n=3 (the default cell size) interpolation would invent
// a value no sample produced.
func percentile(values []float64, p float64) (float64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	if p <= 0 {
		return sorted[0], true
	}
	rank := int(ceil(p / 100 * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1], true
}

func summarize(name string, values []float64) metricSummary {
	summary := metricSummary{Name: name, N: len(values)}
	if len(values) == 0 {
		return summary
	}
	summary.Available = true
	p50, _ := percentile(values, 50)
	p90, _ := percentile(values, 90)
	lo, _ := percentile(values, 0)
	hi, _ := percentile(values, 100)
	summary.P50, summary.P90, summary.Min, summary.Max = &p50, &p90, &lo, &hi
	return summary
}

// ceil avoids importing math for one call and keeps the rounding rule visible.
func ceil(f float64) float64 {
	truncated := float64(int64(f))
	if f > truncated {
		return truncated + 1
	}
	return truncated
}
