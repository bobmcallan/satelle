package verb

import (
	"strings"
	"testing"
	"time"
)

// sty_ef8a896b: --time takes a duration OR a bare number of minutes, and a
// malformed value is refused with an example of each accepted shape rather than
// time.ParseDuration's bare "missing unit in duration".
func TestParseCostDuration(t *testing.T) {
	ok := []struct {
		in   string
		want time.Duration
	}{
		{"38", 38 * time.Minute},
		{" 38 ", 38 * time.Minute},
		{"1.5", 90 * time.Second},
		{"38m", 38 * time.Minute},
		{"2h", 2 * time.Hour},
		{"1h30m", 90 * time.Minute},
		{"45s", 45 * time.Second},
	}
	for _, c := range ok {
		got, err := parseCostDuration(c.in)
		if err != nil || got != c.want {
			t.Errorf("parseCostDuration(%q) = (%v, %v), want %v", c.in, got, err, c.want)
		}
	}
	// Malformed and non-positive input is refused, and the message carries a
	// working example of both accepted shapes.
	for _, bad := range []string{"38x", "abc", "", "  ", "0", "-5m", "-38"} {
		_, err := parseCostDuration(bad)
		if err == nil {
			t.Errorf("parseCostDuration(%q) should error", bad)
			continue
		}
		for _, want := range []string{"30m", "38"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("parseCostDuration(%q) error %q should include the example %q", bad, err, want)
			}
		}
	}
}

// Rounding is pinned rather than accidental: whole minutes truncate, but any
// positive duration under a minute records as 1 rather than 0 (sty_ef8a896b).
func TestCostMinutes(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want int
	}{
		{38 * time.Minute, 38},
		{2 * time.Hour, 120},
		{90 * time.Second, 1},
		{45 * time.Second, 1},
		{time.Nanosecond, 1},
		{0, 0},
	}
	for _, c := range cases {
		if got := costMinutes(c.in); got != c.want {
			t.Errorf("costMinutes(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}
