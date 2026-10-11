package costview

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestAmbiguousRowsAreClassifiedOnce pins sty_6deecf76 AC3: a driver row whose session
// another story shared over the window is available but ambiguous — excluded from the
// exact token and cost sums, counted as AmbiguousRows beside BackfilledRows, carried
// through Fold, and labelled with AmbiguousLabel.
func TestAmbiguousRowsAreClassifiedOnce(t *testing.T) {
	exact := DriverRow{SessionID: "s1", Executable: "claude", Available: true, FreshInput: 10, Output: 1, CostUSD: f64(1)}
	ambiguous := DriverRow{
		SessionID: "s1", Executable: "claude", Available: true, FreshInput: 700, CacheRead: 70, CacheWrite: 7, Output: 77,
		CostUSD: f64(9), Trigger: "close",
		Overlaps:          []string{"sty_other"},
		AttributionReason: "claude: window overlaps sty_other in session s1; usage not split",
	}
	if !ambiguous.Ambiguous() || exact.Ambiguous() || ambiguous.measured() {
		t.Fatalf("classification: ambiguous=%v exact=%v measured=%v", ambiguous.Ambiguous(), exact.Ambiguous(), ambiguous.measured())
	}
	// An unavailable row that names overlaps is a gap, not an ambiguous figure.
	if (DriverRow{Available: false, Overlaps: []string{"x"}}).Ambiguous() {
		t.Fatal("an unavailable row must never classify as ambiguous")
	}

	// A fresh-baseline row that also names overlaps stays a baseline gap: its zero
	// delta is not an "ambiguous" figure, and it keeps the baseline reason.
	baseline := DriverRow{SessionID: "s1", Executable: "claude", Available: true, BaselineFresh: true, Overlaps: []string{"sty_other"}}
	if baseline.Ambiguous() || baseline.measured() || baseline.unmeasuredReason() != BaselineFreshReason("claude") {
		t.Fatalf("baseline+overlaps: ambiguous=%v measured=%v reason=%q, want the named baseline gap", baseline.Ambiguous(), baseline.measured(), baseline.unmeasuredReason())
	}
	bacc := &accumulator{}
	bacc.addDriverRow(baseline)
	if bf := bacc.figures(); bf.UsageRows != 0 || bf.AmbiguousTokens != 0 {
		t.Fatalf("baseline+overlaps figures = %+v, want it in neither exact nor ambiguous sums", bf)
	}

	acc := &accumulator{}
	acc.addDriverRow(exact)
	acc.addDriverRow(ambiguous)
	f := acc.figures()
	if f.FreshInput != 10 || f.Output != 1 || f.CacheRead != 0 || f.CacheWrite != 0 || f.UsageRows != 1 || f.UsageUnavailableRows != 0 {
		t.Fatalf("exact sums = %+v, want only the exact row", f)
	}
	if f.CostUSD != 1 || f.CostRows != 1 || f.CostUnavailableRows != 0 {
		t.Fatalf("cost sums = %v rows %d unavailable %d, want only the exact row's $1", f.CostUSD, f.CostRows, f.CostUnavailableRows)
	}
	if f.AmbiguousTokens != 700+70+7+77 || f.AmbiguousCostUSD != 9 {
		t.Fatalf("ambiguous = %d tokens $%v, want 854 / $9 kept apart from the exact sums", f.AmbiguousTokens, f.AmbiguousCostUSD)
	}
	if len(f.Driver) != 1 || f.Driver[0].Rows != 2 || f.Driver[0].UsageRows != 1 || f.Driver[0].AmbiguousRows != 1 || f.DriverStatus != DriverPartial {
		t.Fatalf("coverage = %+v status %q, want 2 rows, 1 usage, 1 ambiguous, partial", f.Driver, f.DriverStatus)
	}
	line := FormatDriverCoverage(f)
	if !strings.Contains(line, "1 ambiguous ("+AmbiguousLabel+")") || strings.Contains(line, "0 of") {
		t.Fatalf("headline = %q", line)
	}
	for _, l := range FormatAdapterCoverage(f) {
		if !strings.Contains(l, "measured (1 of 1 rows)") || !strings.Contains(l, "1 ambiguous ("+AmbiguousLabel+")") {
			t.Errorf("adapter line = %q: the ambiguous row is neither measured nor a gap", l)
		}
	}

	// Carried through Fold across the family.
	merged := Fold(f, f)
	if merged.Driver[0].AmbiguousRows != 2 || merged.AmbiguousTokens != 2*854 || merged.AmbiguousCostUSD != 18 || merged.FreshInput != 20 || merged.DriverStatus != DriverPartial {
		t.Fatalf("fold = %+v", merged)
	}

	// A story whose only rows are ambiguous has no exact figure at all.
	only := &accumulator{}
	only.addDriverRow(ambiguous)
	g := only.figures()
	if g.HasRows() || g.DriverStatus != DriverPartial {
		t.Fatalf("all-ambiguous figures = %+v, want no exact rows and partial", g)
	}
	if got := FormatDriverCoverage(g); !strings.Contains(got, "1 ambiguous ("+AmbiguousLabel+")") || strings.Contains(got, "()") {
		t.Fatalf("all-ambiguous headline = %q", got)
	}
	if lines := FormatAdapterUsageCoverage(g); len(lines) != 1 || !strings.Contains(lines[0], "none exact") {
		t.Fatalf("all-ambiguous adapter line = %q", lines)
	}
}

// TestAmbiguousRowsRenderOutsideTheTotal: the driver table shows an ambiguous row's own
// figures and labels it, but the TOTAL row counts only the exact rows. The qualifier an
// adapter attaches is carried as the row's note.
func TestAmbiguousRowsRenderOutsideTheTotal(t *testing.T) {
	rows := []DriverRow{
		{SessionID: "s1", Executable: "grok", Available: true, FreshInput: 5, Output: 2, Trigger: "close", CostUSD: f64(0.5),
			AttributionReason: "grok: subagent use undeterminable; subagent spend inclusion unverified"},
		{SessionID: "s1", Executable: "grok", Available: true, FreshInput: 900, Output: 90, Trigger: "close", CostUSD: f64(4),
			Overlaps: []string{"sty_a", "sty_b"}, AttributionReason: "grok: window overlaps sty_a, sty_b in session s1; usage not split"},
	}
	views, total := FormatDriverRows(rows)
	if len(views) != 2 || total == nil {
		t.Fatalf("views = %d total = %v", len(views), total)
	}
	if views[0].Trigger != "close" || views[0].Note != rows[0].AttributionReason {
		t.Errorf("qualified exact row = %+v, want an unlabelled trigger and the qualifier as its note", views[0])
	}
	if views[1].Trigger != "close ("+AmbiguousLabel+")" || views[1].FreshIn != "900" || views[1].Note != rows[1].AttributionReason {
		t.Errorf("ambiguous row = %+v, want its own figures, the label, and the overlap note", views[1])
	}
	if total.FreshIn != "5" || total.Out != "2" || total.USD != "$0.50" {
		t.Errorf("total = %+v, want only the exact row", total)
	}
}

// TestOwnReadsAmbiguousFromTheLedgerRow: a driver_usage payload carrying overlaps
// decodes to an ambiguous row, so Own — the figures behind `story cost`, the web page and
// measured_actual — keeps it out of the exact sums.
func TestOwnReadsAmbiguousFromTheLedgerRow(t *testing.T) {
	base := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	payload := func(fresh int, overlaps []string) []byte {
		p := map[string]any{"session_id": "s", "executable": "claude", "available": true, "fresh_input": fresh, "output": 1, "wall_seconds": 1.0}
		if overlaps != nil {
			p["overlaps"] = overlaps
			p["attribution_reason"] = "claude: window overlaps sty_x in session s; usage not split"
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	entries := []ledger.Entry{
		{StoryID: "sty_1", Kind: ledger.KindDriverUsage, Payload: payload(100, nil), CreatedAt: base},
		{StoryID: "sty_1", Kind: ledger.KindDriverUsage, Payload: payload(5000, []string{"sty_x"}), CreatedAt: base.Add(time.Minute)},
	}
	story := Own(workitem.Item{ID: "sty_1", CreatedAt: base}, entries, Clock{}, base.Add(time.Hour))
	if story.Figures.FreshInput != 100 || story.Figures.AmbiguousTokens != 5001 || story.Figures.Driver[0].AmbiguousRows != 1 {
		t.Fatalf("figures = %+v, want 100 exact fresh tokens and the 5000-token row held apart", story.Figures)
	}
	if len(story.DriverRows) != 2 || !story.DriverRows[1].Ambiguous() || story.DriverRows[1].AttributionReason == "" {
		t.Fatalf("driver rows = %+v", story.DriverRows)
	}
}
