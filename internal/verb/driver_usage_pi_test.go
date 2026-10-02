package verb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// piSeam drives one story through story-set on a pi session whose record is read
// from the real reader (not a stub), with the pi agent dir isolated. When record
// is empty no session file is written, so the reader must say so by name.
func piSeam(t *testing.T, record string) StoryCost {
	t.Helper()
	db := wireDU(t)
	const session = "01a0f01e-fb15-7095-8798-9b7ab9d3d092"
	const repo = "/home/example/pi-repo"
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv(config.SessionEnv, session)
	t.Setenv("CLAUDECODE", "") // a pi session must not be read as claude
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	config.PublishSessionModel(session, SessionModelRoleInLoop, "unknown", agentcli.HarnessPi, "pi hook payload")
	if record != "" {
		body, err := os.ReadFile(filepath.Join("..", "agentcli", "testdata", "driver", record))
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(agentDir, "sessions", "--home-example-pi-repo--")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "2026-09-30T02-22-37-333Z_"+session+".jsonl"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prev := driverSnapshotter
	driverSnapshotter = func(harness, sessionID, _ string) agentcli.DriverSnapshot {
		return agentcli.SessionUsageSnapshot(harness, sessionID, repo)
	}
	t.Cleanup(func() { driverSnapshotter = prev })

	ctx := context.Background()
	create, _ := json.Marshal(map[string]any{"title": "pi driven", "status": "backlog"})
	resp, err := Dispatch(ctx, "story-create", create)
	if err != nil {
		t.Fatal(err)
	}
	var it struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp, &it); err != nil {
		t.Fatal(err)
	}
	set, _ := json.Marshal(map[string]any{"id": it.ID, "status": "in_progress"})
	if _, err := Dispatch(ctx, "story-set", set); err != nil {
		t.Fatal(err)
	}
	if record != "" {
		// The session keeps working after the engage baseline: one more real-shaped
		// assistant row (100 fresh, 10 out, 50 cache read) lands before the close.
		f, err := os.OpenFile(filepath.Join(agentDir, "sessions", "--home-example-pi-repo--", "2026-09-30T02-22-37-333Z_"+session+".jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString(`{"type":"message","id":"z1","message":{"role":"assistant","model":"stealth/space-bunny-alpha","usage":{"input":100,"output":10,"cacheRead":50,"cacheWrite":0,"totalTokens":160,"cost":{"total":0}}}}` + "\n")
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	done, _ := json.Marshal(map[string]any{"id": it.ID, "status": "done"})
	if _, err := Dispatch(ctx, "story-set", done); err != nil {
		t.Fatal(err)
	}
	rows := driverUsageRows(t, db, it.ID)
	if len(rows) != 2 || rows[0].Executable != agentcli.HarnessPi || rows[1].Executable != agentcli.HarnessPi {
		t.Fatalf("rows = %+v, want two driver_usage rows with executable pi", rows)
	}
	sc, err := ComputeStoryCost(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

// TestPiDrivenStoryActualIncludesDriver: a story driven on pi writes a driver row
// read from pi's own session record, and the story's figures say the driver is
// measured — the driving session is in the total, and the report says so.
func TestPiDrivenStoryActualIncludesDriver(t *testing.T) {
	sc := piSeam(t, "pi_session.jsonl")
	// The engage row is the session's baseline; the close row carries the delta
	// the session spent while the story was engaged.
	base, d := sc.DriverRows[0], sc.DriverRows[1]
	if !base.Available || base.Cumulative.FreshInput != 14263 || base.Cumulative.Output != 351 || base.Cumulative.CacheRead != 21128 {
		t.Fatalf("baseline row = %+v, want pi's summed cumulative usage", base)
	}
	if !d.Available || d.FreshInput != 100 || d.Output != 10 || d.CacheRead != 50 {
		t.Fatalf("driver row = %+v, want the 100/10/50 delta", d)
	}
	if d.CostUSD != nil || !strings.HasPrefix(d.CostUnavailableReason, "pi:") {
		t.Fatalf("cost = %v (%q), want an unavailable pi cost, not a measured zero", d.CostUSD, d.CostUnavailableReason)
	}
	f := sc.Figures
	if f.DriverStatus != costview.DriverMeasured || len(f.Driver) != 1 || f.Driver[0].Executable != agentcli.HarnessPi {
		t.Fatalf("driver coverage = %q %+v, want measured pi", f.DriverStatus, f.Driver)
	}
	if f.FreshInput != 100 || f.Output != 10 {
		t.Fatalf("figures fresh/out = %d/%d: the driving session is not in the story actual", f.FreshInput, f.Output)
	}
	if line := costview.FormatDriverCoverage(f); line != "driver: measured (pi 2 rows)" {
		t.Fatalf("coverage line = %q", line)
	}
}

// TestPiFreshBaselineAfterUnavailableIsNotAMeasuredZero separates the two things
// that both read as an all-zero delta on a pi close row (sty_89768625): the first
// available reading after unavailable rows (no prior measurement to diff against,
// so the zero is the baseline being set) and an ordinary delta that really is
// zero. Only the former carries BaselineFresh; it reports no model-call count and
// no measured usage, so a session that spent 294 turns is not shown as a driver
// that spent nothing.
func TestPiFreshBaselineAfterUnavailableIsNotAMeasuredZero(t *testing.T) {
	db := wireDU(t)
	const session = "01a0f01e-fb15-7095-8798-9b7ab9d3d092"
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv(config.SessionEnv, session)
	t.Setenv("CLAUDECODE", "")
	config.PublishSessionModel(session, SessionModelRoleInLoop, "unknown", agentcli.HarnessPi, "pi hook payload")

	spent := agentcli.DriverSnapshot{
		Available: true, Executable: agentcli.HarnessPi, Turns: 294, ModelCalls: 294,
		FreshInputTokens: 14263, OutputTokens: 351, CacheReadInputTokens: 21128,
	}
	stubSnapshotter(t,
		agentcli.DriverSnapshot{Executable: agentcli.HarnessPi, UnavailableReason: "pi: session record for x not found"}, // engage: unreadable
		spent, // close: first available reading, session already spent tokens
		spent, // a later transition that really moved nothing
	)

	ctx := context.Background()
	create, _ := json.Marshal(map[string]any{"title": "pi baseline", "status": "backlog"})
	resp, err := Dispatch(ctx, "story-create", create)
	if err != nil {
		t.Fatal(err)
	}
	var it struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp, &it); err != nil {
		t.Fatal(err)
	}
	item := workitem.Item{ID: it.ID, Kind: workitem.KindStory, Status: "plan"}
	now := time.Unix(1_700_000_000, 0)
	recordDriverUsage(ctx, item, "backlog", "plan", now)
	recordDriverUsage(ctx, item, "plan", "done", now.Add(time.Minute))

	rows := driverUsageRows(t, db, it.ID)
	if len(rows) != 2 || rows[0].Available || !rows[1].Available {
		t.Fatalf("rows = %+v, want an unavailable row then an available one", rows)
	}
	first := rows[1]
	if !first.BaselineFresh {
		t.Fatalf("first available row after an unavailable one must be BaselineFresh: %+v", first)
	}
	if first.FreshInput != 0 || first.Turns != 294 || first.BaseTurns != 294 {
		t.Fatalf("row = %+v, want the fresh-baseline shape turns=294 base_turns=294 delta 0", first)
	}
	if first.ModelCalls != nil || first.ModelCallsUnavailableReason != costview.BaselineFreshReason(agentcli.HarnessPi) {
		t.Fatalf("model calls = %v (%q), want none and the pi baseline reason", first.ModelCalls, first.ModelCallsUnavailableReason)
	}
	if first.Pending || first.Unflushed {
		t.Fatalf("a flush-clean pi row is neither Pending nor Unflushed: %+v", first)
	}

	sc, err := ComputeStoryCost(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sc.DriverMeasuredRows != 0 || sc.DriverTotalTokens != 0 {
		t.Fatalf("driver measured rows/tokens = %d/%d: a baseline row is not a measurement", sc.DriverMeasuredRows, sc.DriverTotalTokens)
	}
	if sc.Figures.DriverStatus != costview.DriverUnavailable || sc.Figures.UsageRows != 0 {
		t.Fatalf("driver coverage = %q usage rows %d, want unavailable with nothing measured", sc.Figures.DriverStatus, sc.Figures.UsageRows)
	}
	views, _ := costview.FormatDriverRows(driverRowsForView(t, sc.DriverRows))
	if views[1].FreshIn != "—" || views[1].Out != "—" || views[1].Calls != "—" {
		t.Fatalf("baseline row renders %+v, want em-dashes, never a literal 0", views[1])
	}

	// The same cumulative read again under a different window is the other
	// explanation for an all-zero delta — a real zero — and is NOT marked.
	recordDriverUsage(ctx, item, "done", "plan", now.Add(2*time.Minute))
	rows = driverUsageRows(t, db, it.ID)
	if len(rows) != 3 || rows[2].BaselineFresh || rows[2].FreshInput != 0 || rows[2].ModelCalls == nil || *rows[2].ModelCalls != 0 {
		t.Fatalf("a measured zero after a measured row = %+v, want an unmarked row with a counted 0 model calls", rows)
	}
}

// TestPiClosingTurnIsCountedInTheSameRead replays the real pi in-flight probe
// (sty_89768625) through the real reader and the verb layer. The session file at
// the moment the third tool call ran — the one standing in for the closing
// `satelle story set` — already holds that call's own assistant row, so the close
// row carries the turn in the same read: it is not Pending, not Unflushed, and no
// later read has anything to credit as Late. Engage is read at the first call.
func TestPiClosingTurnIsCountedInTheSameRead(t *testing.T) {
	db := wireDU(t)
	const session = "01a0f01e-fb15-7095-8798-9b7ab9d3d092"
	const repo = "/home/example/pi-repo"
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv(config.SessionEnv, session)
	t.Setenv("CLAUDECODE", "")
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	config.PublishSessionModel(session, SessionModelRoleInLoop, "unknown", agentcli.HarnessPi, "pi hook payload")
	prev := driverSnapshotter
	driverSnapshotter = func(harness, sessionID, _ string) agentcli.DriverSnapshot {
		return agentcli.SessionUsageSnapshot(harness, sessionID, repo)
	}
	t.Cleanup(func() { driverSnapshotter = prev })

	probe, err := os.ReadFile(filepath.Join("..", "agentcli", "testdata", "driver", "pi_inflight_probe.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// atCall is the file as it stood when the k-th tool call executed.
	atCall := func(k int) string {
		var out strings.Builder
		seen := 0
		for _, line := range strings.SplitAfter(string(probe), "\n") {
			out.WriteString(line)
			if strings.Contains(line, `"type":"toolCall"`) {
				if seen++; seen == k {
					break
				}
			}
		}
		return out.String()
	}
	record := filepath.Join(agentDir, "sessions", "--home-example-pi-repo--", "2026-10-01T20-56-27-543Z_"+session+".jsonl")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		t.Fatal(err)
	}
	put := func(k int) {
		if err := os.WriteFile(record, []byte(atCall(k)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	create, _ := json.Marshal(map[string]any{"title": "pi closing turn", "status": "backlog"})
	resp, err := Dispatch(ctx, "story-create", create)
	if err != nil {
		t.Fatal(err)
	}
	var it struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp, &it); err != nil {
		t.Fatal(err)
	}
	item := workitem.Item{ID: it.ID, Kind: workitem.KindStory, Status: "plan"}
	now := time.Unix(1_700_000_000, 0)
	put(1)
	recordDriverUsage(ctx, item, "backlog", "plan", now)
	put(3)
	recordDriverUsage(ctx, item, "plan", "done", now.Add(time.Minute))

	rows := driverUsageRows(t, db, it.ID)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want engage and close", rows)
	}
	closeRow := rows[1]
	if !closeRow.Available || closeRow.Pending || closeRow.Unflushed || closeRow.Late {
		t.Fatalf("close row = %+v, want available and neither Pending, Unflushed nor Late: pi's calling row is in the file", closeRow)
	}
	// Calls 2 and 3 landed between the engage read and the close read; call 3 is the
	// closing turn itself.
	if closeRow.Turns != 3 || closeRow.BaseTurns != 1 {
		t.Fatalf("close row turns=%d base_turns=%d, want 3/1 (the closing turn counted)", closeRow.Turns, closeRow.BaseTurns)
	}
	if closeRow.FreshInput != 40 || closeRow.Output != 242 || closeRow.CacheRead != 5009 {
		t.Fatalf("close row delta = %d/%d/%d, want 40/242/5009 (rows 2 and 3 in full)", closeRow.FreshInput, closeRow.Output, closeRow.CacheRead)
	}
	if closeRow.BaselineFresh {
		t.Fatalf("close row = %+v: a measured delta after a measured engage is not a fresh baseline", closeRow)
	}
}

// driverRowsForView decodes verb's payload rows into the figures-only rows the cost
// views render, through the JSON the ledger carries (a driver_usage entry decodes
// cleanly into either shape).
func driverRowsForView(t *testing.T, rows []DriverUsagePayload) []costview.DriverRow {
	t.Helper()
	body, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	var out []costview.DriverRow
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestPiDrivenStoryUnreadableRecordIsNamedUnavailable: with no session record the
// driver row is an adapter-named unavailable, the figures contribute no tokens,
// and the report says the total is gated-and-dispatched only.
func TestPiDrivenStoryUnreadableRecordIsNamedUnavailable(t *testing.T) {
	sc := piSeam(t, "")
	d := sc.DriverRows[0]
	if d.Available || !strings.HasPrefix(d.UnavailableReason, "pi:") {
		t.Fatalf("driver row = %+v, want an unavailable named for pi", d)
	}
	f := sc.Figures
	if f.DriverStatus != costview.DriverUnavailable || f.FreshInput != 0 || f.UsageRows != 0 {
		t.Fatalf("figures = %+v, want unavailable with nothing measured", f)
	}
	line := costview.FormatDriverCoverage(f)
	if !strings.Contains(line, "driver: unavailable") || !strings.Contains(line, "pi:") || !strings.Contains(line, "gated-and-dispatched") {
		t.Fatalf("coverage line = %q", line)
	}
}
