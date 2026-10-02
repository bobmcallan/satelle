package costview

import (
	"strings"
	"testing"
)

func f64(v float64) *float64 { return &v }

// TestDriverCoverage pins the per-adapter status derived from driver rows: a
// measured claude row and an unavailable pi row are two adapters, each with its
// own reasons, and the roll-up is partial.
func TestDriverCoverage(t *testing.T) {
	acc := &accumulator{}
	acc.addDriverRow(DriverRow{Executable: "claude", Available: true, FreshInput: 10, CostUnavailableReason: "claude: no cost field"})
	acc.addDriverRow(DriverRow{Executable: "pi", Available: false, UnavailableReason: "pi: session record not found", CostUnavailableReason: "pi: session record not found"})
	acc.addDriverRow(DriverRow{Executable: "pi", Available: false, UnavailableReason: "pi: session record not found", CostUnavailableReason: "pi: session record not found"})
	f := acc.figures()

	if f.DriverStatus != DriverPartial {
		t.Fatalf("status = %q, want partial", f.DriverStatus)
	}
	if len(f.Driver) != 2 || f.Driver[0].Executable != "claude" || f.Driver[1].Executable != "pi" {
		t.Fatalf("driver = %+v", f.Driver)
	}
	pi := f.Driver[1]
	if pi.Rows != 2 || pi.UsageRows != 0 || len(pi.UsageUnavailableBy) != 1 {
		t.Fatalf("pi coverage = %+v, want 2 rows, none measured, one distinct reason", pi)
	}
	lines := FormatAdapterCoverage(f)
	want := []string{
		"claude: driver usage measured (1 of 1 rows); driver cost unavailable (claude: no cost field)",
		"pi: driver usage unavailable (pi: session record not found); driver cost unavailable (pi: session record not found)",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("adapter lines =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if got := FormatDriverCoverage(f); !strings.HasPrefix(got, "driver: partial — pi: session record not found (2 of 2 rows)") ||
		!strings.Contains(got, "gated-and-dispatched") {
		t.Fatalf("headline = %q", got)
	}
}

// TestDriverCoverageStatuses covers the four statuses, and that "none" is stated
// rather than left as silence.
func TestDriverCoverageStatuses(t *testing.T) {
	cases := []struct {
		name string
		rows []DriverRow
		want string
		line string
	}{
		{"none", nil, DriverNone, "driver: none recorded; total is gated-and-dispatched agents only, not the driving session"},
		{"measured", []DriverRow{{Executable: "grok", Available: true, CostUSD: f64(1)}}, DriverMeasured, "driver: measured (grok 1 rows)"},
		{"unavailable", []DriverRow{{Executable: "", Available: false}}, DriverUnavailable, "driver: unavailable — unknown: no reason recorded (1 of 1 rows); total is gated-and-dispatched agents only, not the driving session"},
	}
	for _, c := range cases {
		acc := &accumulator{}
		for _, r := range c.rows {
			acc.addDriverRow(r)
		}
		f := acc.figures()
		if f.DriverStatus != c.want || FormatDriverCoverage(f) != c.line {
			t.Errorf("%s: status %q line %q, want %q / %q", c.name, f.DriverStatus, FormatDriverCoverage(f), c.want, c.line)
		}
	}
}

// TestFoldMergesDriverCoverage: a family total carries every child's adapters,
// summed, so the aggregate cannot claim a driver a child never had.
func TestFoldMergesDriverCoverage(t *testing.T) {
	a := &accumulator{}
	a.addDriverRow(DriverRow{Executable: "claude", Available: true})
	b := &accumulator{}
	b.addDriverRow(DriverRow{Executable: "pi", Available: false, UnavailableReason: "pi: gone"})
	c := &accumulator{}
	c.addDriverRow(DriverRow{Executable: "pi", Available: true})

	total := Fold(Fold(a.figures(), b.figures()), c.figures())
	if total.DriverStatus != DriverPartial || len(total.Driver) != 2 {
		t.Fatalf("total = %q %+v", total.DriverStatus, total.Driver)
	}
	if pi := total.Driver[1]; pi.Executable != "pi" || pi.Rows != 2 || pi.UsageRows != 1 || pi.UsageUnavailableBy[0] != "pi: gone" {
		t.Fatalf("pi = %+v", pi)
	}
	if none := Fold(Figures{}, Figures{}); none.DriverStatus != DriverNone {
		t.Fatalf("empty fold status = %q, want none", none.DriverStatus)
	}
}

// TestBackfilledRowsAreDerivedNotMeasured pins sty_8c0e7e8c: a backfilled row counts as
// a usage row but never reads as live — the status is derived, the headline says how many
// rows were backfilled, and an unavailable row a later backfill recovered is superseded
// (kept for its wall time, dropped from the gap count) only for the session it recovered.
func TestBackfilledRowsAreDerivedNotMeasured(t *testing.T) {
	rows := MarkSuperseded([]DriverRow{
		{SessionID: "s1", Executable: "pi", WallSeconds: 5, UnavailableReason: "pi: no driver-usage reader for this harness"},
		{SessionID: "s2", Executable: "pi", UnavailableReason: "pi: session record not found"},
		{SessionID: "s1", Executable: "pi", Available: true, Backfilled: true, FreshInput: 7, Trigger: "backfill"},
	})
	if !rows[0].Superseded || rows[1].Superseded || rows[2].Superseded {
		t.Fatalf("superseded = %v %v %v, want only s1's earlier unavailable row", rows[0].Superseded, rows[1].Superseded, rows[2].Superseded)
	}
	acc := &accumulator{}
	for _, d := range rows {
		acc.addDriverRow(d)
	}
	f := acc.figures()
	if f.DriverMs != 5000 || f.FreshInput != 7 || f.UsageRows != 1 {
		t.Fatalf("figures = %+v, want the superseded row's wall time kept and the backfill's tokens counted", f)
	}
	if f.DriverStatus != DriverPartial || f.Driver[0].Rows != 2 || f.Driver[0].BackfilledRows != 1 {
		t.Fatalf("status/coverage = %q %+v: s2's unavailable row is still a gap", f.DriverStatus, f.Driver)
	}
	if got := FormatDriverCoverage(f); !strings.Contains(got, "1 backfilled ("+BackfilledLabel+")") {
		t.Fatalf("partial headline = %q", got)
	}

	only := &accumulator{}
	only.addDriverRow(DriverRow{Executable: "pi", Available: true, Backfilled: true, FreshInput: 1})
	g := only.figures()
	if g.DriverStatus != DriverDerived {
		t.Fatalf("status = %q, want derived", g.DriverStatus)
	}
	if got := FormatDriverCoverage(g); got != "driver: derived (pi 1 rows; 1 backfilled ("+BackfilledLabel+"))" {
		t.Fatalf("derived headline = %q", got)
	}
	merged := Fold(g, g)
	if merged.Driver[0].BackfilledRows != 2 || merged.DriverStatus != DriverDerived {
		t.Fatalf("fold = %+v", merged)
	}
}
