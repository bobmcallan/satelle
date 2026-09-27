package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func float64p(v float64) *float64 { return &v }

// costFixture builds the SAME tiny parent+child family internal/web's
// TestCostVMRendersSharedFixture renders — reused (not re-derived) so the CLI
// and web tests are proven against ONE shared fixture (sty_b8542a3a AC7),
// never two independently hand-typed ones that could quietly drift apart.
func costFixture(t *testing.T) (workitem.Item, costview.Story, costview.FamilyCost) {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := costview.Clock{
		Engaging: func(to string) bool { return to == "in_progress" },
		Terminal: func(to string) bool { return to == "done" },
	}
	priced := 1.5
	invoke := func(storyID string, at time.Time, cost *float64, fresh, out int) ledger.Entry {
		p := map[string]any{
			"from": "plan", "to": "in_progress", "agent": "coder", "skill": "coder", "model": "sonnet",
			"tokens_in_fresh": fresh, "tokens_out": out, "tokens_in": fresh, "tokens_total": fresh + out,
			"usage_available": true, "duration_ms": 1000,
		}
		if cost != nil {
			p["cost_usd"] = *cost
		}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		return ledger.Entry{StoryID: storyID, Kind: ledger.KindAgentInvocation, Payload: raw, CreatedAt: at}
	}
	transition := func(storyID, from, to string, at time.Time) ledger.Entry {
		raw, _ := json.Marshal(map[string]any{"from": from, "to": to})
		return ledger.Entry{StoryID: storyID, Kind: ledger.KindStatusTransition, Payload: raw, CreatedAt: at}
	}

	root := workitem.Item{ID: "sty_root", CreatedAt: base, Tags: []string{"estimate-usd:10"}}
	child := workitem.Item{ID: "sty_child", ParentID: "sty_root", CreatedAt: base}
	items := []workitem.Item{root, child}

	entriesByID := map[string][]ledger.Entry{
		"sty_root": {
			transition("sty_root", "backlog", "in_progress", base),
			transition("sty_root", "in_progress", "done", base.Add(time.Hour)),
			invoke("sty_root", base.Add(5*time.Minute), &priced, 100, 20),
		},
		"sty_child": {
			transition("sty_child", "backlog", "in_progress", base.Add(10*time.Minute)),
			transition("sty_child", "in_progress", "done", base.Add(20*time.Minute)),
			invoke("sty_child", base.Add(15*time.Minute), nil, 10, 2),
		},
	}
	now := base.Add(2 * time.Hour)
	own := costview.Own(root, entriesByID["sty_root"], clk, now)
	fam := costview.Family(root, items, entriesByID, func(workitem.Item) costview.Clock { return clk }, now)
	return root, own, fam
}

// TestPrintCostSummaryRendersSharedFixture pins AC7 from the CLI side: fed the
// exact costview.Story/FamilyCost internal/web's TestCostVMRendersSharedFixture
// renders into HTML, printCostSummary prints the SAME $, fresh, elapsed, agent
// time and family-total figures — both surfaces read one costview computation.
func TestPrintCostSummaryRendersSharedFixture(t *testing.T) {
	_, own, fam := costFixture(t)
	sc := verb.StoryCost{Figures: own.Figures, Estimates: own.Estimates, Family: &fam}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	printCostSummary(cmd, sc)
	out := buf.String()

	for _, want := range []string{
		"$1.50",
		"est. $10.00",
		"110",            // FAMILY TOTAL fresh input: 100 (root) + 10 (child)
		"1 unavailable)", // FAMILY TOTAL uncosted count: root priced, child uncosted
		"FAMILY TOTAL",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("printCostSummary missing %q; got:\n%s", want, out)
		}
	}
}

// TestPrintCostSummaryEmptyChildShowsUnavailable pins the orchestrator's third
// finding: a family child with no dispatch/driver rows at all (e.g. still
// backlog) must print '—' for fresh/output/cache, never a literal 0 that
// would misrepresent "nothing measured" as "measured nothing".
func TestPrintCostSummaryEmptyChildShowsUnavailable(t *testing.T) {
	fam := costview.FamilyCost{
		Root: costview.Story{ID: "sty_root"},
		Children: []costview.Story{
			{ID: "sty_empty", Figures: costview.Figures{ElapsedMs: -1}},
		},
		Total: costview.Figures{ElapsedMs: -1},
	}
	sc := verb.StoryCost{Figures: costview.Figures{ElapsedMs: -1}, Family: &fam}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	printCostSummary(cmd, sc)
	out := buf.String()

	if !strings.Contains(out, "sty_empty") {
		t.Fatalf("printCostSummary missing the empty child row; got:\n%s", out)
	}
	var sawEmptyChild, sawFamilyTotal bool
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		switch {
		case strings.HasPrefix(line, "sty_empty"):
			sawEmptyChild = true
		case strings.HasPrefix(line, "FAMILY TOTAL"):
			sawFamilyTotal = true
		default:
			continue
		}
		if strings.Contains(line, "0") && !strings.Contains(line, "unavailable") {
			t.Errorf("row should show unavailable, not a literal 0: %q", line)
		}
		if !strings.Contains(strings.Join(fields, " "), "unavailable") {
			t.Errorf("row missing unavailable for its no-rows figures: %q", line)
		}
	}
	if !sawEmptyChild || !sawFamilyTotal {
		t.Fatalf("did not find both the empty child and FAMILY TOTAL rows; got:\n%s", out)
	}
}

// TestPrintDriverSectionSplitsColumns pins the rework fix: the DRIVER SESSION
// table reports fresh/output/cache read/cache write as separate columns (never
// one cache-inclusive TOKENS figure), plus each row's own agent time, and an
// unavailable row renders every figure as '—' rather than a folded-in 0.
func TestPrintDriverSectionSplitsColumns(t *testing.T) {
	sc := verb.StoryCost{
		DriverRows: []verb.DriverUsagePayload{
			{
				SessionID: "sess1", Executable: "claude", Trigger: "engage",
				FreshInput: 100, Output: 20, CacheRead: 10, CacheWrite: 5,
				Available: true, WallSeconds: 90, CostUSD: float64p(1.25),
			},
			{
				SessionID: "sess2", Executable: "codex", Trigger: "close",
				Available: false, WallSeconds: 30,
			},
		},
	}
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := printDriverSection(cmd, sc); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if !strings.Contains(out, "FRESH IN") || !strings.Contains(out, "CACHE READ") || !strings.Contains(out, "CACHE WRITE") {
		t.Fatalf("DRIVER SESSION header missing split columns; got:\n%s", out)
	}
	if strings.Contains(out, "TOKENS") {
		t.Fatalf("DRIVER SESSION table still has a cache-inclusive TOKENS column; got:\n%s", out)
	}
	for _, want := range []string{"100", "20", "10", "5", "$1.25", "1m30s", "—"} {
		if !strings.Contains(out, want) {
			t.Errorf("printDriverSection missing %q; got:\n%s", want, out)
		}
	}
}

// TestPrintDriverSectionAllUnavailableTotalNeverZero pins the AC1 regression
// fix: when every driver row is unavailable, the TOTAL row's fresh/output/
// cache columns must read "unavailable", never a literal "0" that would
// misrepresent "nothing measured" as "measured zero".
func TestPrintDriverSectionAllUnavailableTotalNeverZero(t *testing.T) {
	sc := verb.StoryCost{
		DriverRows: []verb.DriverUsagePayload{
			{SessionID: "sess1", Executable: "codex", Trigger: "engage", Available: false, WallSeconds: 10},
			{SessionID: "sess2", Executable: "codex", Trigger: "close", Available: false, WallSeconds: 5},
		},
	}
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := printDriverSection(cmd, sc); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	var totalLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "TOTAL") {
			totalLine = line
		}
	}
	if totalLine == "" {
		t.Fatalf("no TOTAL line found; got:\n%s", out)
	}
	if strings.Contains(totalLine, "0 (measured") || strings.Contains(strings.Fields(totalLine)[1], "0") {
		t.Errorf("TOTAL line shows unavailable as a literal 0: %q", totalLine)
	}
	if !strings.Contains(totalLine, "unavailable") {
		t.Errorf("TOTAL line should read unavailable when nothing was measured: %q", totalLine)
	}
}

// TestPrintDriverSectionGrandTotalSplitsColumns pins the remaining AC1 rework
// finding: GRAND TOTAL must read off costview's single-owner Figures (fresh/
// out/cache read/cache write kept separate) rather than the legacy
// GrandTotalTokens blend, so it never leads with one cache-inclusive number.
func TestPrintDriverSectionGrandTotalSplitsColumns(t *testing.T) {
	sc := verb.StoryCost{
		DriverRows: []verb.DriverUsagePayload{
			{SessionID: "sess1", Executable: "claude", Trigger: "engage", Available: true, WallSeconds: 10},
		},
		// Legacy fields a caller might still populate for JSON back-compat —
		// GRAND TOTAL must NOT read these; it must read Figures instead.
		GrandTotalTokens:  999999,
		GrandTotalCostUSD: 999.99,
		Figures: costview.Figures{
			CostUSD: 1.5, CostRows: 1, CostUnavailableRows: 1,
			FreshInput: 100, Output: 20, CacheRead: 10, CacheWrite: 5,
			UsageRows: 1,
			AgentMs:   5000,
		},
	}
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := printDriverSection(cmd, sc); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	var grandTotalLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "GRAND TOTAL") {
			grandTotalLine = line
		}
	}
	if grandTotalLine == "" {
		t.Fatalf("no GRAND TOTAL line found; got:\n%s", out)
	}
	if strings.Contains(grandTotalLine, "999999") || strings.Contains(grandTotalLine, "999.99") {
		t.Errorf("GRAND TOTAL read the legacy blended fields instead of Figures: %q", grandTotalLine)
	}
	// Assert the $ FIELD exactly (not merely a substring): a stray literal '$'
	// left in the format string around FormatUSD's own leading '$' would print
	// "$$1.50 (+1 unavailable)" and still pass a bare strings.Contains(line,
	// "$1.50") check.
	if !strings.Contains(grandTotalLine, "): $1.50 (+1 unavailable) |") {
		t.Errorf("GRAND TOTAL $ field != $1.50 (+1 unavailable) (possible doubled '$'); got: %q", grandTotalLine)
	}
	if strings.Contains(grandTotalLine, "$$") {
		t.Errorf("GRAND TOTAL has a doubled '$': %q", grandTotalLine)
	}
	for _, want := range []string{"fresh in 100", "out 20", "cache read 10", "cache write 5"} {
		if !strings.Contains(grandTotalLine, want) {
			t.Errorf("GRAND TOTAL missing %q; got: %q", want, grandTotalLine)
		}
	}
}

// TestPrintDriverSectionGrandTotalNeverZeroWhenUnmeasured pins the "can show
// 0" half of the same finding: an all-unavailable Figures (nothing measured
// at all) must render GRAND TOTAL as unavailable, never a literal 0.
func TestPrintDriverSectionGrandTotalNeverZeroWhenUnmeasured(t *testing.T) {
	sc := verb.StoryCost{
		DriverRows: []verb.DriverUsagePayload{
			{SessionID: "sess1", Executable: "codex", Trigger: "engage", Available: false, WallSeconds: 10},
		},
	}
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := printDriverSection(cmd, sc); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	var grandTotalLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "GRAND TOTAL") {
			grandTotalLine = line
		}
	}
	if grandTotalLine == "" {
		t.Fatalf("no GRAND TOTAL line found; got:\n%s", out)
	}
	if strings.Contains(grandTotalLine, "fresh in 0") || strings.Contains(grandTotalLine, "$0.00") {
		t.Errorf("GRAND TOTAL shows unavailable as a literal 0: %q", grandTotalLine)
	}
	if !strings.Contains(grandTotalLine, "unavailable") {
		t.Errorf("GRAND TOTAL should read unavailable when nothing was measured: %q", grandTotalLine)
	}
}
