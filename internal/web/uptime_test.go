package web

import (
	"testing"
	"time"
)

// TestFormatUptimeBoundaries pins the three formatUptime bands (sty_efeb2a69):
// < 1m → "up Ns", < 1h → "up Nm", >= 1h → "up Hh Mm". This is the render-time
// uptime snapshot the header shows (see serverStart).
func TestFormatUptimeBoundaries(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "up 0s"},
		{45 * time.Second, "up 45s"},
		{59 * time.Second, "up 59s"},
		{time.Minute, "up 1m"},                        // boundary: first minute
		{90 * time.Second, "up 1m"},                   // truncates to whole minutes
		{59*time.Minute + 59*time.Second, "up 59m"},   // just under an hour
		{time.Hour, "up 1h 0m"},                       // boundary: first hour
		{2*time.Hour + 5*time.Minute, "up 2h 5m"},     // hours + remainder minutes
		{25*time.Hour + 30*time.Minute, "up 25h 30m"}, // hours accumulate past a day
		{3*time.Hour + 59*time.Minute, "up 3h 59m"},   // minute remainder, not rolled over
	}
	for _, c := range cases {
		if got := formatUptime(c.d); got != c.want {
			t.Errorf("formatUptime(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}
