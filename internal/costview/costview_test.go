package costview_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func invocationEntry(t *testing.T, storyID string, at time.Time, skill string, costUSD *float64, fresh, cacheWrite, cacheRead, output int, usageAvailable bool, durationMs int64) ledger.Entry {
	t.Helper()
	payload := map[string]any{
		"from": "plan", "to": "in_progress", "agent": "coder", "skill": skill, "model": "sonnet",
		"tokens_in_fresh": fresh, "tokens_cache_write": cacheWrite, "tokens_cache_read": cacheRead,
		"tokens_out": output, "tokens_in": fresh + cacheWrite + cacheRead, "tokens_total": fresh + cacheWrite + cacheRead + output,
		"usage_available": usageAvailable, "duration_ms": durationMs,
	}
	if costUSD != nil {
		payload["cost_usd"] = *costUSD
	}
	return ledger.Entry{StoryID: storyID, Kind: ledger.KindAgentInvocation, Payload: mustJSON(t, payload), CreatedAt: at}
}

func transitionEntry(storyID, from, to string, at time.Time) ledger.Entry {
	return ledger.Entry{StoryID: storyID, Kind: ledger.KindStatusTransition,
		Payload: json.RawMessage(`{"from":"` + from + `","to":"` + to + `"}`), CreatedAt: at}
}

// fixtureClock treats "in_progress" as engaging and "done" as terminal —
// "blocked" is neither, so a park never stops the clock.
func fixtureClock() costview.Clock {
	return costview.Clock{
		Engaging: func(to string) bool { return to == "in_progress" },
		Terminal: func(to string) bool { return to == "done" },
	}
}

// TestOwnDollarsAndSplit pins AC1: dollars sum only costed rows, uncosted rows
// are counted rather than folded in as zero, and cache write never lands in
// fresh input or cache read.
func TestOwnDollarsAndSplit(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	priced := 1.5
	entries := []ledger.Entry{
		invocationEntry(t, "sty_1", base, "coder", &priced, 100, 20, 30, 50, true, 1000),
		invocationEntry(t, "sty_1", base.Add(time.Minute), "reviewer", nil, 10, 0, 5, 2, true, 500),
	}
	item := workitem.Item{ID: "sty_1", CreatedAt: base}
	story := costview.Own(item, entries, fixtureClock(), base.Add(2*time.Minute))

	if story.Figures.CostUSD != 1.5 || story.Figures.CostRows != 1 || story.Figures.CostUnavailableRows != 1 {
		t.Fatalf("dollars = %+v, want sum 1.5, costed 1, uncosted 1", story.Figures)
	}
	if story.Figures.FreshInput != 110 || story.Figures.CacheWrite != 20 || story.Figures.CacheRead != 35 {
		t.Fatalf("split = fresh %d cacheWrite %d cacheRead %d, want 110/20/35",
			story.Figures.FreshInput, story.Figures.CacheWrite, story.Figures.CacheRead)
	}
	if story.Figures.Output != 52 {
		t.Fatalf("output = %d, want 52", story.Figures.Output)
	}
	if got := costview.FormatUSD(story.Figures.CostUSD, story.Figures.CostRows, story.Figures.CostUnavailableRows); got != "$1.50 (+1 unavailable)" {
		t.Fatalf("FormatUSD = %q", got)
	}
	if got := costview.FormatUSD(0, 0, 0); got != "unavailable" {
		t.Fatalf("FormatUSD(no rows) = %q, want unavailable (never $0.00)", got)
	}
}

// TestOwnPricedButUsageUnreportedNeverPrintsZero pins the rework fix: a row
// can be PRICED (cost_usd present) while its provider reports no usage at all
// (usage_available: false) — cost availability must never stand in for token
// availability. Figures.HasRows() (and the headline FreshInput/Output/
// CacheRead/CacheWrite it gates) must read as unavailable, not a literal 0,
// even though CostRows is nonzero.
func TestOwnPricedButUsageUnreportedNeverPrintsZero(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	priced := 2.0
	entries := []ledger.Entry{
		invocationEntry(t, "sty_1", base, "coder", &priced, 0, 0, 0, 0, false, 1000),
	}
	item := workitem.Item{ID: "sty_1", CreatedAt: base}
	story := costview.Own(item, entries, fixtureClock(), base.Add(time.Minute))

	if story.Figures.CostUSD != 2.0 || story.Figures.CostRows != 1 {
		t.Fatalf("cost = %+v, want the row's $2.00 still priced", story.Figures)
	}
	if story.Figures.HasRows() {
		t.Fatal("HasRows = true, want false — the row is priced but reported no usage")
	}
	if story.Figures.UsageRows != 0 || story.Figures.UsageUnavailableRows != 1 {
		t.Fatalf("usage rows = %d measured / %d unavailable, want 0/1", story.Figures.UsageRows, story.Figures.UsageUnavailableRows)
	}
	for _, n := range []int{story.Figures.FreshInput, story.Figures.Output, story.Figures.CacheRead, story.Figures.CacheWrite} {
		if n != 0 {
			t.Fatalf("token figure = %d, want 0 (unmeasured, never folded in)", n)
		}
	}
	got := costview.FormatTokensMeasured(story.Figures.FreshInput, story.Figures.UsageRows, story.Figures.UsageUnavailableRows)
	if got != "unavailable (1 unreported)" {
		t.Fatalf("FormatTokensMeasured = %q, want \"unavailable (1 unreported)\" — a priced-but-unmeasured row must never render as a literal 0", got)
	}
}

// TestOwnAllLegacyUnsplitFreshInputNeverPrintsZero pins the second rework
// fix: a row can report REAL usage (usage_available: true, a nonzero
// tokens_in/tokens_out) while predating the fresh/cache split schema — its
// tokens land in UnsplitTokens, not FreshInput/CacheRead/CacheWrite. Those
// three figures must read "unavailable (N unsplit)" when every measured row
// is legacy-unsplit like this (e.g. the dogfood's reviewer@opus: 53 rows, 0
// fresh, 33725570 unsplit) — never a literal 0, which would misrepresent
// "never split out" as "measured zero fresh work". Output is unaffected: it
// is always real regardless of the split.
func TestOwnAllLegacyUnsplitFreshInputNeverPrintsZero(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	legacy := ledger.Entry{StoryID: "sty_1", Kind: ledger.KindAgentInvocation, CreatedAt: base,
		Payload: mustJSON(t, map[string]any{
			"agent": "reviewer", "skill": "reviewer", "model": "opus",
			"tokens_in": 5000, "tokens_out": 200, "usage_available": true,
			// no tokens_in_fresh/tokens_cache_write/tokens_cache_read: pre-split row
		})}
	item := workitem.Item{ID: "sty_1", CreatedAt: base}
	story := costview.Own(item, []ledger.Entry{legacy}, fixtureClock(), base.Add(time.Minute))

	if story.Figures.UsageRows != 1 || story.Figures.UnsplitRows != 1 {
		t.Fatalf("usage rows = %d, unsplit rows = %d, want 1/1", story.Figures.UsageRows, story.Figures.UnsplitRows)
	}
	if story.Figures.UnsplitTokens != 5000 {
		t.Fatalf("UnsplitTokens = %d, want 5000", story.Figures.UnsplitTokens)
	}
	if story.Figures.FreshInput != 0 || story.Figures.CacheRead != 0 || story.Figures.CacheWrite != 0 {
		t.Fatalf("split figures = %+v, want all 0 (never split out of the legacy row)", story.Figures)
	}
	splitRows := story.Figures.UsageRows - story.Figures.UnsplitRows
	for _, tc := range []struct {
		name string
		n    int
	}{{"FreshInput", story.Figures.FreshInput}, {"CacheRead", story.Figures.CacheRead}, {"CacheWrite", story.Figures.CacheWrite}} {
		got := costview.FormatSplitTokens(tc.n, splitRows, story.Figures.UnsplitRows, story.Figures.UsageUnavailableRows)
		if got != "unavailable (1 unsplit)" {
			t.Errorf("FormatSplitTokens(%s) = %q, want \"unavailable (1 unsplit)\", not a literal 0", tc.name, got)
		}
	}
	// Output is real regardless of the split — it must NOT read unavailable.
	if got := costview.FormatTokensMeasured(story.Figures.Output, story.Figures.UsageRows, story.Figures.UsageUnavailableRows); got != "200" {
		t.Errorf("FormatTokensMeasured(Output) = %q, want 200 (output is always real, split or not)", got)
	}
}

// TestOwnElapsedVsAgentTime pins AC2: elapsed is the shape-derived clock
// (engage to terminal), never substituted by agent time, and a park does not
// stop the clock.
func TestOwnElapsedVsAgentTime(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entries := []ledger.Entry{
		transitionEntry("sty_1", "backlog", "in_progress", base),
		transitionEntry("sty_1", "in_progress", "blocked", base.Add(30*time.Minute)),
		transitionEntry("sty_1", "blocked", "in_progress", base.Add(45*time.Minute)),
		transitionEntry("sty_1", "in_progress", "done", base.Add(2*time.Hour)),
		invocationEntry(t, "sty_1", base.Add(5*time.Minute), "coder", nil, 10, 0, 0, 5, true, 10*60*1000),
	}
	item := workitem.Item{ID: "sty_1", CreatedAt: base.Add(-time.Hour)}
	story := costview.Own(item, entries, fixtureClock(), base.Add(3*time.Hour))

	if story.Figures.ElapsedMs != (2 * time.Hour).Milliseconds() {
		t.Fatalf("elapsed = %dms, want 2h (blocked must not stop the clock)", story.Figures.ElapsedMs)
	}
	if story.Figures.AgentMs != (10 * time.Minute).Milliseconds() {
		t.Fatalf("agent time = %dms, want 10m", story.Figures.AgentMs)
	}
	if story.Figures.AgentMs == story.Figures.ElapsedMs {
		t.Fatal("agent time must not equal elapsed — they are never substituted for each other")
	}
}

// TestOwnNeverEngagedElapsedUnavailable pins the AC2 rework: a story sitting
// in a non-engaging, non-terminal state (e.g. still backlog) with no logged
// engage transition has never started its clock — ElapsedMs must read as
// unavailable (-1, per FormatDuration), never a CreatedAt-to-now span that
// would claim the untouched story has been running for hours. It also has no
// dispatch/driver rows at all, so HasRows is false and the token figures
// render as unavailable rather than a literal 0.
func TestOwnNeverEngagedElapsedUnavailable(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	item := workitem.Item{ID: "sty_1", Status: "backlog", CreatedAt: base.Add(-12 * time.Hour)}
	story := costview.Own(item, nil, fixtureClock(), base)

	if story.Figures.ElapsedMs >= 0 {
		t.Fatalf("elapsed = %dms, want a negative (unavailable) sentinel for a never-engaged story", story.Figures.ElapsedMs)
	}
	if got := costview.FormatDuration(story.Figures.ElapsedMs); got != "unavailable" {
		t.Fatalf("FormatDuration(elapsed) = %q, want unavailable", got)
	}
	if story.Figures.HasRows() {
		t.Fatal("HasRows = true, want false — no dispatch/driver row was ever recorded")
	}
	if got := costview.FormatTokensFigure(story.Figures.FreshInput, story.Figures.HasRows()); got != "unavailable" {
		t.Fatalf("FormatTokensFigure(fresh input) = %q, want unavailable, not a literal 0", got)
	}
	if story.Span != nil {
		t.Fatalf("span = %+v, want nil — the clock never started", story.Span)
	}
}

// TestOwnCancelledFromBacklogElapsedUnavailable pins the second rework fix: a
// story cancelled straight out of backlog (backlog -> cancelled is logged,
// terminal, but NO engaging transition was ever entered) must not report an
// elapsed span at all — the backlog-to-cancel gap is not work time, and
// item's current terminal status must never stand in for having engaged.
func TestOwnCancelledFromBacklogElapsedUnavailable(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entries := []ledger.Entry{
		transitionEntry("sty_1", "backlog", "done", base.Add(3*time.Hour)), // fixtureClock treats "done" as terminal
	}
	item := workitem.Item{ID: "sty_1", Status: "done", CreatedAt: base}
	story := costview.Own(item, entries, fixtureClock(), base.Add(4*time.Hour))

	if story.Figures.ElapsedMs >= 0 {
		t.Fatalf("elapsed = %dms, want unavailable — cancelled/closed from backlog without ever engaging", story.Figures.ElapsedMs)
	}
	if story.Span != nil {
		t.Fatalf("span = %+v, want nil — the clock never started", story.Span)
	}
}

// TestOwnLegacyTerminalNoTransitionsElapsedUnavailable pins the same fix for
// a legacy item: currently terminal, but with NO transitions logged at all
// (its history predates ledger tracking) — CreatedAt is not proof of when
// work started, so elapsed must read unavailable, not a fabricated
// CreatedAt-to-now span that grows every day the story sits untouched.
func TestOwnLegacyTerminalNoTransitionsElapsedUnavailable(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	item := workitem.Item{ID: "sty_1", Status: "done", CreatedAt: base.Add(-90 * 24 * time.Hour)}
	story := costview.Own(item, nil, fixtureClock(), base)

	if story.Figures.ElapsedMs >= 0 {
		t.Fatalf("elapsed = %dms, want unavailable — no transition ever proves this item engaged", story.Figures.ElapsedMs)
	}
}

// TestFamilyNeverEngagedChildDoesNotInflateTotal pins the orchestrator's
// family-level regression: a backlog child with no ledger rows must not push
// its CreatedAt-to-now span into the family's union, nor print a literal 0
// for its token figures.
func TestFamilyNeverEngagedChildDoesNotInflateTotal(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	root := workitem.Item{ID: "sty_root", Status: "done", CreatedAt: base}
	child := workitem.Item{ID: "sty_c1", ParentID: "sty_root", Status: "backlog", CreatedAt: base.Add(-30 * 24 * time.Hour)}
	items := []workitem.Item{root, child}

	entriesByID := map[string][]ledger.Entry{
		"sty_root": {
			transitionEntry("sty_root", "backlog", "in_progress", base),
			transitionEntry("sty_root", "in_progress", "done", base.Add(20*time.Minute)),
		},
		"sty_c1": nil, // never engaged: no ledger rows at all
	}
	now := base.Add(2 * time.Hour)
	fam := costview.Family(root, items, entriesByID, func(workitem.Item) costview.Clock { return fixtureClock() }, now)

	if len(fam.Children) != 1 {
		t.Fatalf("children = %d, want 1", len(fam.Children))
	}
	childFigures := fam.Children[0].Figures
	if childFigures.ElapsedMs >= 0 {
		t.Fatalf("child elapsed = %dms, want unavailable — it never engaged", childFigures.ElapsedMs)
	}
	if childFigures.HasRows() {
		t.Fatal("child HasRows = true, want false")
	}
	want := (20 * time.Minute).Milliseconds()
	if fam.Total.ElapsedMs != want {
		t.Fatalf("family total elapsed = %dms, want %dms (root's own span alone; the never-engaged child contributes no span)",
			fam.Total.ElapsedMs, want)
	}
}

// TestFamilyNestedEpicNeverEngagedMiddleUsesDescendantUnion pins the sharpest
// case of the AC2/AC5 rework: a middle node that is ITSELF a nested epic
// (has its own children) but never engaged on its own account (the sentinel,
// -1) must still report its subtree's UNION span as its family-row elapsed —
// never the Fold SUM, which would both (a) corrupt the sentinel by ordinary
// arithmetic and (b) double-count its two children's heavily overlapping
// windows.
func TestFamilyNestedEpicNeverEngagedMiddleUsesDescendantUnion(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	root := workitem.Item{ID: "sty_root", CreatedAt: base}
	middle := workitem.Item{ID: "sty_mid", ParentID: "sty_root", Status: "backlog", CreatedAt: base}
	gcA := workitem.Item{ID: "sty_gca", ParentID: "sty_mid", CreatedAt: base}
	gcB := workitem.Item{ID: "sty_gcb", ParentID: "sty_mid", CreatedAt: base}
	items := []workitem.Item{root, middle, gcA, gcB}

	entriesByID := map[string][]ledger.Entry{
		"sty_root": {
			transitionEntry("sty_root", "backlog", "in_progress", base),
			transitionEntry("sty_root", "in_progress", "done", base.Add(100*time.Minute)),
		},
		"sty_mid": nil, // never engaged itself — a pure grouping epic
		"sty_gca": {
			transitionEntry("sty_gca", "backlog", "in_progress", base.Add(10*time.Minute)),
			transitionEntry("sty_gca", "in_progress", "done", base.Add(40*time.Minute)),
		},
		"sty_gcb": { // overlaps sty_gca's window heavily
			transitionEntry("sty_gcb", "backlog", "in_progress", base.Add(15*time.Minute)),
			transitionEntry("sty_gcb", "in_progress", "done", base.Add(45*time.Minute)),
		},
	}
	now := base.Add(2 * time.Hour)
	fam := costview.Family(root, items, entriesByID, func(workitem.Item) costview.Clock { return fixtureClock() }, now)

	var mid *costview.Story
	for i := range fam.Children {
		if fam.Children[i].ID == "sty_mid" {
			mid = &fam.Children[i]
		}
	}
	if mid == nil {
		t.Fatal("sty_mid not found in fam.Children")
	}
	want := (35 * time.Minute).Milliseconds() // union 10m-45m, NOT the 60m sum of two 30m spans
	if mid.Figures.ElapsedMs != want {
		t.Fatalf("sty_mid elapsed = %dms, want %dms (union of its never-engaged self [nil] with its children's union, not a Fold sum)",
			mid.Figures.ElapsedMs, want)
	}
}

// TestFormatDriverRowsAllUnavailableIsNeverZero pins the AC1/AC7 rework: when
// every driver row is unavailable, FormatDriverRows' TOTAL fold must read
// unavailable for fresh/output/cache, never a literal 0 that would claim a
// measured zero — this is the ONE formatting pass both the CLI's DRIVER
// SESSION table and the web page's Driver sessions table call, so neither
// surface can independently reintroduce the "0 (measured; N of N unreported)"
// bug the previous round shipped.
func TestFormatDriverRowsAllUnavailableIsNeverZero(t *testing.T) {
	rows := []costview.DriverRow{
		{SessionID: "sess1", Executable: "nosuch", Trigger: "engage", Available: false, WallSeconds: 10},
		{SessionID: "sess2", Executable: "nosuch", Trigger: "close", Available: false, WallSeconds: 5},
	}
	rowViews, total := costview.FormatDriverRows(rows)
	if len(rowViews) != 2 || total == nil {
		t.Fatalf("rowViews = %+v, total = %+v, want 2 rows + a total", rowViews, total)
	}
	for _, got := range []string{total.FreshIn, total.Out, total.CacheRead, total.CacheWrite} {
		if got != "unavailable" {
			t.Errorf("total column = %q, want unavailable (never a literal 0)", got)
		}
	}
	if total.USD != "unavailable (2 rows)" {
		t.Errorf("total USD = %q, want unavailable (2 rows) — never a $0.00", total.USD)
	}

	if rows, total := costview.FormatDriverRows(nil); rows != nil || total != nil {
		t.Errorf("FormatDriverRows(nil) = %+v/%+v, want nil/nil", rows, total)
	}
}

// TestFormatDriverRowsMixedMeasuresOnlyAvailableRows pins the split itself:
// fresh/output/cache read/cache write stay separate columns in the fold, and
// an unavailable row contributes nothing to the sum (never a folded-in 0).
func TestFormatDriverRowsMixedMeasuresOnlyAvailableRows(t *testing.T) {
	cost := 1.25
	rows := []costview.DriverRow{
		{SessionID: "sess1", Executable: "claude", Trigger: "engage",
			FreshInput: 100, Output: 20, CacheRead: 10, CacheWrite: 5,
			Available: true, WallSeconds: 90, CostUSD: &cost},
		{SessionID: "sess2", Executable: "nosuch", Trigger: "close", Available: false, WallSeconds: 30},
	}
	rowViews, total := costview.FormatDriverRows(rows)
	if rowViews[0].FreshIn != "100" || rowViews[0].Out != "20" || rowViews[0].CacheRead != "10" || rowViews[0].CacheWrite != "5" {
		t.Fatalf("available row = %+v, want split 100/20/10/5", rowViews[0])
	}
	if rowViews[1].FreshIn != "—" || rowViews[1].Out != "—" {
		t.Fatalf("unavailable row = %+v, want '—' for every figure", rowViews[1])
	}
	if total.FreshIn != "100" || total.Out != "20" || total.CacheRead != "10" || total.CacheWrite != "5" {
		t.Fatalf("total = %+v, want the single available row's split, unaffected by the unavailable row", total)
	}
	if total.USD != "$1.25 (+1 unavailable)" {
		t.Fatalf("total USD = %q, want $1.25 (+1 unavailable)", total.USD)
	}
}

// TestParseEstimatesKeepsUnit pins AC3: a legacy estimate stays in the unit it
// was written in and is never folded into a measured figure's cell.
func TestParseEstimatesKeepsUnit(t *testing.T) {
	estimates := costview.ParseEstimates([]string{"estimate-tokens:5000", "epic:x"})
	if len(estimates) != 1 || estimates[0].Unit != "tokens" || estimates[0].Value != 5000 {
		t.Fatalf("estimates = %+v, want one tokens:5000", estimates)
	}
	if got := costview.FormatEstimate(estimates, "usd"); got != "" {
		t.Fatalf("FormatEstimate(usd) with only a tokens estimate = %q, want empty", got)
	}
	if got := costview.FormatLegacyTokenEstimate(estimates); got != "estimate (legacy, cache-inclusive tokens): 5000" {
		t.Fatalf("FormatLegacyTokenEstimate = %q", got)
	}

	fresh := costview.ParseEstimates([]string{"estimate-usd:25", "estimate-fresh-input:20000", "estimate-output:250000"})
	if got := costview.FormatEstimate(fresh, "usd"); got != "est. $25.00" {
		t.Fatalf("FormatEstimate(usd) = %q", got)
	}
	if got := costview.FormatEstimate(fresh, "fresh-input"); got != "est. 20000" {
		t.Fatalf("FormatEstimate(fresh-input) = %q", got)
	}
}

// TestOwnDriverRowIsSeparate pins AC4: a driver row's figures are folded into
// AgentMs/tokens but Own also reports it as its own DriverRows entry, never
// silently merged with dispatch Rows.
func TestOwnDriverRowIsSeparate(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cost := 2.0
	driverPayload := map[string]any{
		"session_id": "sess1", "executable": "claude", "fresh_input": 100, "output": 20,
		"cache_read": 10, "cache_write": 5, "available": true, "wall_seconds": 60.0, "cost_usd": cost,
	}
	entries := []ledger.Entry{
		{StoryID: "sty_1", Kind: ledger.KindDriverUsage, Payload: mustJSON(t, driverPayload), CreatedAt: base},
	}
	item := workitem.Item{ID: "sty_1", CreatedAt: base}
	story := costview.Own(item, entries, fixtureClock(), base.Add(time.Hour))

	if len(story.DriverRows) != 1 || len(story.Rows) != 0 {
		t.Fatalf("driver rows = %d, dispatch rows = %d, want 1 and 0", len(story.DriverRows), len(story.Rows))
	}
	if story.Figures.DriverMs != 60000 || story.Figures.DispatchMs != 0 {
		t.Fatalf("driverMs=%d dispatchMs=%d, want 60000/0", story.Figures.DriverMs, story.Figures.DispatchMs)
	}
	if story.Figures.FreshInput != 100 || story.Figures.CostUSD != 2.0 {
		t.Fatalf("figures = %+v, want fresh 100 cost 2.0", story.Figures)
	}
}

// TestFamilyRollup pins AC5: a parent with children and a grandchild folds
// into per-child lines and a family total whose elapsed is the UNION of spans,
// not their sum, and whose trigger is having children, not a category tag.
func TestFamilyRollup(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	root := workitem.Item{ID: "sty_root", ParentID: "", CreatedAt: base}
	child1 := workitem.Item{ID: "sty_c1", ParentID: "sty_root", CreatedAt: base}
	child2 := workitem.Item{ID: "sty_c2", ParentID: "sty_root", CreatedAt: base}
	grandchild := workitem.Item{ID: "sty_gc1", ParentID: "sty_c1", CreatedAt: base}
	items := []workitem.Item{root, child1, child2, grandchild}

	priced := 1.0
	entriesByID := map[string][]ledger.Entry{
		"sty_root": {
			transitionEntry("sty_root", "backlog", "in_progress", base),
			transitionEntry("sty_root", "in_progress", "done", base.Add(90*time.Minute)),
		},
		"sty_c1": {
			transitionEntry("sty_c1", "backlog", "in_progress", base.Add(10*time.Minute)),
			transitionEntry("sty_c1", "in_progress", "done", base.Add(40*time.Minute)),
			invocationEntry(t, "sty_c1", base.Add(15*time.Minute), "coder", &priced, 10, 0, 0, 5, true, 1000),
		},
		"sty_c2": {
			transitionEntry("sty_c2", "backlog", "in_progress", base.Add(20*time.Minute)),
			transitionEntry("sty_c2", "in_progress", "done", base.Add(1*time.Hour)),
			invocationEntry(t, "sty_c2", base.Add(25*time.Minute), "coder", nil, 5, 0, 0, 1, true, 500),
		},
		"sty_gc1": {
			transitionEntry("sty_gc1", "backlog", "in_progress", base.Add(5*time.Minute)),
			transitionEntry("sty_gc1", "in_progress", "done", base.Add(35*time.Minute)),
		},
	}
	now := base.Add(2 * time.Hour)
	fam := costview.Family(root, items, entriesByID, func(workitem.Item) costview.Clock { return fixtureClock() }, now)

	if len(fam.Children) != 2 {
		t.Fatalf("children = %d, want 2 (grandchild folds into sty_c1)", len(fam.Children))
	}
	if fam.Total.CostRows != 1 || fam.Total.CostUnavailableRows != 1 {
		t.Fatalf("total costed/uncosted = %d/%d, want 1/1", fam.Total.CostRows, fam.Total.CostUnavailableRows)
	}
	if fam.Total.CostUSD != 1.0 {
		t.Fatalf("total cost = %v, want 1.0", fam.Total.CostUSD)
	}
	// Union span: root's own engage (0) to root's own terminal (+90m) — the
	// widest bound, since every child terminates before it — proving the
	// family clock is a UNION of spans (root's plus every child's), not a sum
	// of the individual clocks (which would total well over 90m here).
	want := (90 * time.Minute).Milliseconds()
	if fam.Total.ElapsedMs != want {
		t.Fatalf("family elapsed = %dms, want %dms (union of spans, not a sum)", fam.Total.ElapsedMs, want)
	}

	// sty_c1 is itself a nested parent (it has grandchild sty_gc1). Its OWN
	// family-row elapsed must ALSO be a union — own span 10m-40m unioned with
	// sty_gc1's 5m-35m gives 5m-40m (35m) — never the Fold SUM of the two
	// 30-minute spans (60m), which double-counts their overlapping window.
	var c1 *costview.Story
	for i := range fam.Children {
		if fam.Children[i].ID == "sty_c1" {
			c1 = &fam.Children[i]
		}
	}
	if c1 == nil {
		t.Fatal("sty_c1 not found in fam.Children")
	}
	wantC1 := (35 * time.Minute).Milliseconds()
	if c1.Figures.ElapsedMs != wantC1 {
		t.Fatalf("sty_c1 (nested epic) elapsed = %dms, want %dms (union of its own span + sty_gc1's, not their 60m sum)",
			c1.Figures.ElapsedMs, wantC1)
	}

	// A root with no children returns an empty Children slice — no family
	// section — regardless of any category tag (trigger is the walk alone).
	lonely := costview.Family(root, []workitem.Item{root}, entriesByID, func(workitem.Item) costview.Clock { return fixtureClock() }, now)
	if len(lonely.Children) != 0 {
		t.Fatalf("lonely.Children = %d, want 0", len(lonely.Children))
	}
}

// TestGateValueAcceptsRejectsAndCostPerReject pins AC6: invocations, dollars,
// accepts, rejects and dollars-per-reject group by skill and seat, the date
// filter drops out-of-range rows, and $/reject uses the known-dollar subtotal.
func TestGateValueAcceptsRejectsAndCostPerReject(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	priced := 4.0
	reviewAccept := ledger.Entry{StoryID: "sty_1", Kind: ledger.KindReviewAccept, CreatedAt: base,
		Payload: mustJSON(t, map[string]any{"skill": "satelle-story-plan-review", "agent": "reviewer", "model": "opus", "accept": true})}
	reviewReject := ledger.Entry{StoryID: "sty_1", Kind: ledger.KindReviewReject, CreatedAt: base.Add(time.Minute),
		Payload: mustJSON(t, map[string]any{"skill": "satelle-story-plan-review", "agent": "reviewer", "model": "opus", "accept": false})}
	outOfRange := ledger.Entry{StoryID: "sty_1", Kind: ledger.KindReviewReject, CreatedAt: base.Add(-24 * time.Hour),
		Payload: mustJSON(t, map[string]any{"skill": "satelle-story-plan-review", "agent": "reviewer", "model": "opus", "accept": false})}
	invocation := ledger.Entry{StoryID: "sty_1", Kind: ledger.KindAgentInvocation, CreatedAt: base,
		Payload: mustJSON(t, map[string]any{
			"from": "plan", "to": "in_progress", "agent": "reviewer", "skill": "satelle-story-plan-review", "model": "opus",
			"tokens_in_fresh": 100, "tokens_out": 50, "tokens_in": 100, "tokens_total": 150,
			"usage_available": true, "duration_ms": 1000, "cost_usd": priced,
		})}
	entriesByID := map[string][]ledger.Entry{
		"sty_1": {invocation, reviewAccept, reviewReject, outOfRange},
	}
	report := costview.GateValue(entriesByID, costview.GateFilter{Since: base.Add(-time.Hour), Until: base.Add(time.Hour)})
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (out-of-range row dropped)", len(report.Rows))
	}
	row := report.Rows[0]
	if row.Skill != "satelle-story-plan-review" || row.Seat != "reviewer@opus" {
		t.Fatalf("row = %+v, want skill satelle-story-plan-review seat reviewer@opus", row)
	}
	if row.Invocations != 1 || row.Accepts != 1 || row.Rejects != 1 {
		t.Fatalf("row = %+v, want 1 invocation, 1 accept, 1 reject", row)
	}
	if row.CostUSD != 4.0 || row.Costed != 1 {
		t.Fatalf("row cost = %v/%d, want 4.0/1", row.CostUSD, row.Costed)
	}
	if got := costview.FormatCostPerReject(row.CostUSD, row.Costed, row.Uncosted, row.Rejects); got != "$4.00" {
		t.Fatalf("FormatCostPerReject = %q, want $4.00", got)
	}
	if got := costview.FormatCostPerReject(0, 0, 0, 0); got != "n/a" {
		t.Fatalf("FormatCostPerReject(no rejects) = %q, want n/a", got)
	}
	// AC6 rework: --json must carry dollars-per-reject too, not just the raw
	// figures a caller would have to re-derive by hand.
	if row.CostPerRejectUSD == nil || *row.CostPerRejectUSD != 4.0 {
		t.Fatalf("row.CostPerRejectUSD = %v, want 4.0", row.CostPerRejectUSD)
	}

	// JSON round-trip: the report marshals to the same shape the table renders from.
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"cost_per_reject_usd":4`) {
		t.Fatalf("JSON missing cost_per_reject_usd; got:\n%s", raw)
	}
	var round costview.GateValueReport
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	if len(round.Rows) != 1 || round.Rows[0].Skill != row.Skill {
		t.Fatalf("round-trip = %+v, want matching row", round)
	}
	if round.Rows[0].CostPerRejectUSD == nil || *round.Rows[0].CostPerRejectUSD != 4.0 {
		t.Fatalf("round-trip CostPerRejectUSD = %v, want 4.0", round.Rows[0].CostPerRejectUSD)
	}
}

// TestGateValueNoRejectsCostPerRejectNil pins the division-by-zero guard: a
// row with rejects==0 must carry a nil CostPerRejectUSD, never a fabricated
// zero (division by zero rejects is not a cost of zero).
func TestGateValueNoRejectsCostPerRejectNil(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	priced := 4.0
	invocation := ledger.Entry{StoryID: "sty_1", Kind: ledger.KindAgentInvocation, CreatedAt: base,
		Payload: mustJSON(t, map[string]any{
			"agent": "reviewer", "skill": "satelle-story-plan-review", "model": "opus",
			"tokens_in_fresh": 100, "usage_available": true, "cost_usd": priced,
		})}
	report := costview.GateValue(map[string][]ledger.Entry{"sty_1": {invocation}}, costview.GateFilter{})
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(report.Rows))
	}
	if report.Rows[0].CostPerRejectUSD != nil {
		t.Fatalf("CostPerRejectUSD = %v, want nil (no rejects)", report.Rows[0].CostPerRejectUSD)
	}
}

// TestGateValueLegacyUnsplitTokensNotCountedAsFresh pins the second rework
// fix: a legacy agent_invocation row recorded before the fresh/cache split
// existed (TokensIn is cache-inclusive) must fold into UnsplitTokens, never
// into FreshTokens — folding it into FreshTokens would misrepresent reused
// cache context as fresh work.
func TestGateValueLegacyUnsplitTokensNotCountedAsFresh(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	legacy := ledger.Entry{StoryID: "sty_1", Kind: ledger.KindAgentInvocation, CreatedAt: base,
		Payload: mustJSON(t, map[string]any{
			"agent": "reviewer", "skill": "satelle-story-plan-review", "model": "opus",
			"tokens_in": 5000, "usage_available": true, // no tokens_in_fresh/cache_write/cache_read: pre-split row
		})}
	report := costview.GateValue(map[string][]ledger.Entry{"sty_1": {legacy}}, costview.GateFilter{})
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(report.Rows))
	}
	row := report.Rows[0]
	if row.FreshTokens != 0 {
		t.Fatalf("FreshTokens = %d, want 0 — a cache-inclusive legacy row is not fresh", row.FreshTokens)
	}
	if row.UnsplitTokens != 5000 {
		t.Fatalf("UnsplitTokens = %d, want 5000", row.UnsplitTokens)
	}
}

// TestGateValueEmptySkillCorrelatesInvocationAndVerdict pins the third rework
// fix: an invocation row and its verdict row must land in the SAME skill/seat
// row even when Skill is empty on both — previously the invocation branch
// alone fell back to Agent as the skill key while the verdict branch never
// did, silently splitting a gate's spend from its verdicts.
func TestGateValueEmptySkillCorrelatesInvocationAndVerdict(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	priced := 2.0
	invocation := ledger.Entry{StoryID: "sty_1", Kind: ledger.KindAgentInvocation, CreatedAt: base,
		Payload: mustJSON(t, map[string]any{
			"agent": "reviewer", "model": "opus", // no "skill" field at all
			"tokens_in_fresh": 10, "usage_available": true, "cost_usd": priced,
		})}
	reject := ledger.Entry{StoryID: "sty_1", Kind: ledger.KindReviewReject, CreatedAt: base.Add(time.Minute),
		Payload: mustJSON(t, map[string]any{"agent": "reviewer", "model": "opus", "accept": false})}
	report := costview.GateValue(map[string][]ledger.Entry{"sty_1": {invocation, reject}}, costview.GateFilter{})
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (invocation and verdict must correlate into one row)", len(report.Rows))
	}
	row := report.Rows[0]
	if row.Invocations != 1 || row.Rejects != 1 {
		t.Fatalf("row = %+v, want 1 invocation and 1 reject in the SAME row", row)
	}
	if row.CostPerRejectUSD == nil || *row.CostPerRejectUSD != 2.0 {
		t.Fatalf("CostPerRejectUSD = %v, want 2.0 — spend and verdict correlated", row.CostPerRejectUSD)
	}
}

// TestGateValueEmptySkillGroupsUnderRole pins AC1: an invocation with no skill
// but a recorded non-gate role is grouped under a label naming that role.
func TestGateValueEmptySkillGroupsUnderRole(t *testing.T) {
	for _, role := range []string{"coder", "orchestrator", "reviewer-consult", "planner"} {
		t.Run(role, func(t *testing.T) {
			inv := ledger.Entry{StoryID: "sty_1", Kind: ledger.KindAgentInvocation, CreatedAt: time.Now(),
				Payload: mustJSON(t, map[string]any{"agent": role, "model": "claude-sonnet-5", "usage_available": false})}
			report := costview.GateValue(map[string][]ledger.Entry{"sty_1": {inv}}, costview.GateFilter{})
			if len(report.Rows) != 1 {
				t.Fatalf("rows = %d, want 1", len(report.Rows))
			}
			if got := report.Rows[0].Skill; got != role || got == "unknown" {
				t.Fatalf("skill = %q, want %q", got, role)
			}
		})
	}
}

// gateValueLabelFixture holds one row of each kind: a named skill, a role with
// no skill, and neither.
func gateValueLabelFixture(t *testing.T) map[string][]ledger.Entry {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func(p map[string]any) ledger.Entry {
		return ledger.Entry{StoryID: "sty_1", Kind: ledger.KindAgentInvocation, CreatedAt: base, Payload: mustJSON(t, p)}
	}
	return map[string][]ledger.Entry{"sty_1": {
		mk(map[string]any{"skill": "plan-review", "agent": "reviewer", "model": "opus", "cost_usd": 1.0}),
		mk(map[string]any{"agent": "orchestrator", "model": "opus", "cost_usd": 2.0}),
		mk(map[string]any{"cost_usd": 3.0}),
	}}
}

// TestGateValueUnknownOnlyWhenNoSkillNorRole pins AC2: "unknown" is reserved
// for rows with neither skill nor role; each kind lands in its own group.
func TestGateValueUnknownOnlyWhenNoSkillNorRole(t *testing.T) {
	report := costview.GateValue(gateValueLabelFixture(t), costview.GateFilter{})
	got := map[string]float64{}
	for _, r := range report.Rows {
		got[r.Skill] = r.CostUSD
	}
	want := map[string]float64{"plan-review": 1.0, "orchestrator": 2.0, "unknown": 3.0}
	if len(got) != len(want) || len(report.Rows) != len(want) {
		t.Fatalf("groups = %v (rows %d), want %v", got, len(report.Rows), want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("groups = %v, want %v", got, want)
		}
	}
}

// TestGateValueSeatMergesNamedAndEmptySkill pins AC3: one seat recorded under
// a named skill and under the empty-skill fallback reports as ONE row whose
// dollars are the sum of its former groups.
func TestGateValueSeatMergesNamedAndEmptySkill(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	named := ledger.Entry{StoryID: "sty_1", Kind: ledger.KindAgentInvocation, CreatedAt: base,
		Payload: mustJSON(t, map[string]any{"agent": "coder", "skill": "coder", "model": "claude-sonnet-5", "cost_usd": 3.0})}
	empty := ledger.Entry{StoryID: "sty_1", Kind: ledger.KindAgentInvocation, CreatedAt: base,
		Payload: mustJSON(t, map[string]any{"agent": "coder", "model": "claude-sonnet-5", "cost_usd": 1.5})}
	report := costview.GateValue(map[string][]ledger.Entry{"sty_1": {named, empty}}, costview.GateFilter{})
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %+v, want 1 merged row", report.Rows)
	}
	r := report.Rows[0]
	if r.Skill != "coder" || r.Seat != "coder@claude-sonnet-5" {
		t.Fatalf("row = %s / %s, want coder / coder@claude-sonnet-5", r.Skill, r.Seat)
	}
	if r.CostUSD != 4.5 || r.Invocations != 2 || r.Costed != 2 {
		t.Fatalf("row = %+v, want $4.50 over 2 invocations, 2 costed", r)
	}
}

// TestGateValueJSONCarriesSameGrouping pins AC4: --json marshals the same
// GateValueReport the table renders, so the grouping survives a round trip.
func TestGateValueJSONCarriesSameGrouping(t *testing.T) {
	report := costview.GateValue(gateValueLabelFixture(t), costview.GateFilter{})
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var round costview.GateValueReport
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	if len(round.Rows) != len(report.Rows) {
		t.Fatalf("round-trip rows = %d, want %d", len(round.Rows), len(report.Rows))
	}
	for i, r := range report.Rows {
		if round.Rows[i].Skill != r.Skill || round.Rows[i].Seat != r.Seat || round.Rows[i].CostUSD != r.CostUSD {
			t.Fatalf("row %d round-trip = %+v, want %+v", i, round.Rows[i], r)
		}
	}
}

// TestGateValuePricedButUsageUnreportedNeverPrintsZero pins the rework fix: a
// gate-value row whose only invocation is priced but reports no usage at all
// (usage_available: false) must render FRESH TOKENS as unavailable, never a
// literal 0 that a dogfood run could mistake for a real zero-token gate.
func TestGateValuePricedButUsageUnreportedNeverPrintsZero(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	priced := 3.0
	invocation := ledger.Entry{StoryID: "sty_1", Kind: ledger.KindAgentInvocation, CreatedAt: base,
		Payload: mustJSON(t, map[string]any{
			"agent": "coder", "model": "grok-4.5", "skill": "coder",
			"usage_available": false, "cost_usd": priced,
		})}
	report := costview.GateValue(map[string][]ledger.Entry{"sty_1": {invocation}}, costview.GateFilter{})
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(report.Rows))
	}
	row := report.Rows[0]
	if row.FreshTokens != 0 || row.UsageRows != 0 || row.UsageUnavailableRows != 1 {
		t.Fatalf("row = %+v, want FreshTokens 0, UsageRows 0, UsageUnavailableRows 1", row)
	}
	if got := costview.FormatTokensMeasured(row.FreshTokens, row.UsageRows, row.UsageUnavailableRows); got != "unavailable (1 unreported)" {
		t.Fatalf("FormatTokensMeasured = %q, want \"unavailable (1 unreported)\", not a literal 0", got)
	}
}

// TestGateValueAllLegacyUnsplitFreshTokensNeverPrintsZero pins the second
// rework fix on the gate-value side: a skill/seat whose every invocation is
// measured but legacy-unsplit (real usage, pre-split schema) must render
// FRESH TOKENS as "unavailable (N unsplit)", never a literal 0 — the exact
// shape the 30-day dogfood showed for reviewer@opus (53 rows, 0 fresh,
// 33725570 unsplit).
func TestGateValueAllLegacyUnsplitFreshTokensNeverPrintsZero(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	legacy := ledger.Entry{StoryID: "sty_1", Kind: ledger.KindAgentInvocation, CreatedAt: base,
		Payload: mustJSON(t, map[string]any{
			"agent": "reviewer", "model": "opus", "skill": "reviewer",
			"tokens_in": 5000, "usage_available": true,
		})}
	report := costview.GateValue(map[string][]ledger.Entry{"sty_1": {legacy}}, costview.GateFilter{})
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(report.Rows))
	}
	row := report.Rows[0]
	if row.FreshTokens != 0 || row.UsageRows != 1 || row.UnsplitRows != 1 {
		t.Fatalf("row = %+v, want FreshTokens 0, UsageRows 1, UnsplitRows 1", row)
	}
	splitRows := row.UsageRows - row.UnsplitRows
	got := costview.FormatSplitTokens(row.FreshTokens, splitRows, row.UnsplitRows, row.UsageUnavailableRows)
	if got != "unavailable (1 unsplit)" {
		t.Fatalf("FormatSplitTokens = %q, want \"unavailable (1 unsplit)\", not a literal 0", got)
	}
}

// TestGateValueCostUSDMarshalsNullWhenUncosted pins the JSON-shape rework fix:
// a row where nothing priced (Costed==0) must marshal cost_usd as null, never
// a fabricated $0 a JSON consumer could mistake for a priced zero. A costed
// row still round-trips its real number.
func TestGateValueCostUSDMarshalsNullWhenUncosted(t *testing.T) {
	invocation := ledger.Entry{StoryID: "sty_1", Kind: ledger.KindAgentInvocation, CreatedAt: time.Now(),
		Payload: mustJSON(t, map[string]any{
			"agent": "coder", "model": "grok-4.5", "skill": "coder", "usage_available": false,
		})}
	report := costview.GateValue(map[string][]ledger.Entry{"sty_1": {invocation}}, costview.GateFilter{})
	if len(report.Rows) != 1 || report.Rows[0].Costed != 0 {
		t.Fatalf("rows = %+v, want 1 row with Costed 0", report.Rows)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"cost_usd":null`) {
		t.Fatalf("uncosted row must marshal cost_usd as null; got:\n%s", raw)
	}
	var round costview.GateValueReport
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	if len(round.Rows) != 1 || round.Rows[0].CostUSD != 0 {
		t.Fatalf("round-trip = %+v, want CostUSD 0", round.Rows)
	}
}

// TestDecodeRowExcludesToolPermissionRows pins the shared decode point: a
// tool-permission event masquerading as an agent_invocation never counts.
func TestDecodeRowExcludesToolPermissionRows(t *testing.T) {
	e := ledger.Entry{Kind: ledger.KindAgentInvocation, Payload: mustJSON(t, map[string]any{
		"decided_by": "user", "decision": "allow", "tool": "Bash",
	})}
	if _, ok := costview.DecodeRow(e); ok {
		t.Fatal("DecodeRow accepted a tool-permission row")
	}
}
