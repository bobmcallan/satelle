package verb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestCursorDriverUsageRowCopiesBothReasons: a cursor session's driver_usage row is
// built from the stop-hook record and carries the cursor-named cost reason and the
// cursor-named model-call reason, not a silent gap or a measured zero (sty_a3258bb3).
func TestCursorDriverUsageRowCopiesBothReasons(t *testing.T) {
	db := wireDU(t)
	const session = "de3328ed-ba7c-44f9-9eeb-3e5d59dee4a8"
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv("SATELLE_CURSOR_USAGE_DIR", t.TempDir())
	t.Setenv(config.SessionEnv, session)
	config.PublishSessionModel(session, SessionModelRoleInLoop, "composer-2.5", agentcli.HarnessCursor, "")
	withWiring(t)
	driverSnapshotter = func(harness, sessionID, repo string) agentcli.DriverSnapshot {
		return agentcli.SessionUsageSnapshot(harness, sessionID, repo)
	}

	stop := func(gen string, in, out, read int) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{
			"hook_event_name": "stop", "session_id": session, "generation_id": gen, "model": "composer-2.5",
			"input_tokens": in, "output_tokens": out, "cache_read_tokens": read, "cache_write_tokens": 0,
		})
		if err := agentcli.RecordCursorStop(session, raw); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	create, _ := json.Marshal(map[string]any{"title": "cursor driven", "status": "backlog"})
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

	// Before any stop: a cursor-named unavailable that gives its cause.
	recordDriverUsage(ctx, item, "backlog", "plan", now)
	stop("g1", 25758, 266, 16563)
	recordDriverUsage(ctx, item, "plan", "done", now.Add(time.Minute))

	rows := driverUsageRows(t, db, it.ID)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want two", rows)
	}
	if rows[0].Available || !strings.HasPrefix(rows[0].UnavailableReason, "cursor:") || agentcli.IsNoDriverReaderReason(rows[0].UnavailableReason) {
		t.Errorf("pre-stop row = %+v, want a cursor-named no-stop unavailable", rows[0])
	}
	r := rows[1]
	if !r.Available || r.Executable != agentcli.HarnessCursor || r.Model != "composer-2.5" {
		t.Fatalf("row = %+v, want an available composer-2.5 cursor row", r)
	}
	if r.Cumulative.FreshInput != 25758-16563 || r.Cumulative.CacheRead != 16563 || r.Cumulative.Output != 266 {
		t.Errorf("cumulative = %+v", r.Cumulative)
	}
	if r.CostUSD != nil || !strings.Contains(r.CostUnavailableReason, "cursor") {
		t.Errorf("cost = %v %q, want the cursor-named reason copied", r.CostUSD, r.CostUnavailableReason)
	}
	if r.ModelCalls != nil || !strings.Contains(r.ModelCallsUnavailableReason, "cursor") {
		t.Errorf("model calls = %v %q, want the cursor-named reason copied", r.ModelCalls, r.ModelCallsUnavailableReason)
	}
}
