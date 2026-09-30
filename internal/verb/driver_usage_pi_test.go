package verb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/costview"
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
