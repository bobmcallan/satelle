//go:build plannerbench

package plannerbench

import "testing"

func TestPercentileNearestRank(t *testing.T) {
	if _, ok := percentile(nil, 50); ok {
		t.Fatal("an empty sample has no percentile")
	}
	single := []float64{7}
	for _, p := range []float64{0, 50, 90, 100} {
		got, ok := percentile(single, p)
		if !ok || got != 7 {
			t.Fatalf("p%v of one sample = %v (ok=%v), want 7", p, got, ok)
		}
	}
	// Odd n: nearest rank, no interpolation — with n=3 interpolation would
	// invent a value no sample produced.
	odd := []float64{30, 10, 20}
	for _, tc := range []struct {
		p    float64
		want float64
	}{{0, 10}, {50, 20}, {90, 30}, {100, 30}} {
		if got, _ := percentile(odd, tc.p); got != tc.want {
			t.Errorf("p%v = %v, want %v", tc.p, got, tc.want)
		}
	}
	// Even n.
	even := []float64{40, 10, 30, 20}
	for _, tc := range []struct {
		p    float64
		want float64
	}{{50, 20}, {90, 40}, {100, 40}} {
		if got, _ := percentile(even, tc.p); got != tc.want {
			t.Errorf("even p%v = %v, want %v", tc.p, got, tc.want)
		}
	}
}

func TestPercentileDoesNotMutateItsInput(t *testing.T) {
	values := []float64{3, 1, 2}
	percentile(values, 50)
	if values[0] != 3 || values[1] != 1 || values[2] != 2 {
		t.Fatalf("input reordered: %v", values)
	}
}

func TestSummarizeDistinguishesUnreportedFromZero(t *testing.T) {
	empty := summarize(metricTTFE, nil)
	if empty.Available || empty.P50 != nil || empty.N != 0 {
		t.Fatalf("a metric no sample reported must be unavailable, not zero: %+v", empty)
	}
	zeros := summarize(metricTTFE, []float64{0, 0, 0})
	if !zeros.Available || zeros.P50 == nil || *zeros.P50 != 0 {
		t.Fatalf("a reported zero must stay available: %+v", zeros)
	}
	s := summarize(metricWall, []float64{100, 200, 300})
	if *s.P50 != 200 || *s.P90 != 300 || *s.Min != 100 || *s.Max != 300 || s.N != 3 {
		t.Fatalf("summary = %+v", s)
	}
}

func TestCeilRoundsUpOnlyForFractions(t *testing.T) {
	for in, want := range map[float64]float64{1: 1, 1.0001: 2, 2.5: 3, 3: 3, 0: 0} {
		if got := ceil(in); got != want {
			t.Errorf("ceil(%v) = %v, want %v", in, got, want)
		}
	}
}
