package verb

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestDriverUsageHarnessFromPublishedSessionOverEnv pins the dogfood defect
// found at release: a grok session launched from a Claude Code shell inherits
// CLAUDECODE=1, so env sniffing named it claude and the recorder read a Claude
// transcript that does not exist. The session's own published in-loop row
// (written by hooks that name their harness) wins over the environment.
func TestDriverUsageHarnessFromPublishedSessionOverEnv(t *testing.T) {
	db := wireDU(t)
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv(config.SessionEnv, "01a0e397-grok-session")
	t.Setenv("CLAUDECODE", "1")
	config.PublishSessionModel("01a0e397-grok-session", SessionModelRoleInLoop, "unknown", agentcli.HarnessGrok, "grok's hook payload carries no model")

	var sawHarness string
	withWiring(t)
	driverSnapshotter = func(harness, sessionID, repoRoot string) agentcli.DriverSnapshot {
		sawHarness = harness
		return agentcli.DriverSnapshot{Available: true, FreshInputTokens: 10, OutputTokens: 1, Turns: 1}
	}

	item := workitem.Item{ID: "sty_du_harness", Kind: workitem.KindStory, Status: "ready"}
	recordDriverUsage(context.Background(), item, "backlog", "ready", time.Unix(1_700_000_000, 0))

	if sawHarness != agentcli.HarnessGrok {
		t.Fatalf("reader asked for harness %q, want grok (published row beats inherited CLAUDECODE)", sawHarness)
	}
	rows := driverUsageRows(t, db, item.ID)
	if len(rows) != 1 || rows[0].Executable != agentcli.HarnessGrok {
		t.Fatalf("rows = %+v, want one row with executable grok", rows)
	}

	// With nothing published for the session, the environment is the fallback.
	t.Setenv(config.SessionEnv, "unpublished-session")
	other := workitem.Item{ID: "sty_du_harness_env", Kind: workitem.KindStory, Status: "ready"}
	recordDriverUsage(context.Background(), other, "backlog", "ready", time.Unix(1_700_000_100, 0))
	if sawHarness != agentcli.HarnessClaude {
		t.Fatalf("unpublished session: harness %q, want claude from the environment", sawHarness)
	}
}

// TestStorySetWritesDriverUsageThroughDispatch pins AC3 and AC7 end to end
// (sty_81caa41b): real story-set transitions through the verb dispatch seam —
// not direct recordDriverUsage calls — each write one driver_usage row with the
// session's delta and the trigger for that edge, and a re-issued story-set to
// the same status writes nothing further.
func TestStorySetWritesDriverUsageThroughDispatch(t *testing.T) {
	db := wireDU(t)
	t.Setenv(config.SessionEnv, "sess-du-seam")
	t.Setenv("CLAUDECODE", "1")

	// The test sets the session's cumulative usage before each step; every
	// snapshot the recorder takes during that step reads the same figure.
	cur := agentcli.DriverSnapshot{Available: true, Model: "claude-opus-5-5", FreshInputTokens: 100, OutputTokens: 10, Turns: 1}
	withWiring(t)
	driverSnapshotter = func(harness, sessionID, repoRoot string) agentcli.DriverSnapshot { return cur }
	grow := func(fresh, out int) {
		cur.FreshInputTokens += fresh
		cur.OutputTokens += out
		cur.Turns++
	}

	ctx := context.Background()
	dispatch := func(name string, req map[string]any) workitem.Item {
		t.Helper()
		b, _ := json.Marshal(req)
		resp, err := Dispatch(ctx, name, b)
		if err != nil {
			t.Fatalf("%s %v: %v", name, req, err)
		}
		var it workitem.Item
		if err := json.Unmarshal(resp, &it); err != nil {
			t.Fatal(err)
		}
		return it
	}

	it := dispatch("story-create", map[string]any{"title": "driver usage seam", "status": "backlog",
		"tags": []string{"estimate-minutes:10", "estimate-tokens:1000"}})

	// Each step: the session does some work, then the driver enacts one edge.
	steps := []struct {
		to          string
		fresh, out  int
		wantTrigger string
	}{
		{"in_progress", 50, 5, DriverTriggerEngage},
		{"blocked", 40, 4, DriverTriggerPark},
		{"in_progress", 30, 3, DriverTriggerEngage},
		{"done", 20, 2, DriverTriggerClose},
	}
	for _, s := range steps {
		grow(s.fresh, s.out)
		if got := dispatch("story-set", map[string]any{"id": it.ID, "status": s.to}); got.Status != s.to {
			t.Fatalf("story-set %s: status = %q", s.to, got.Status)
		}
	}

	rows := driverUsageRows(t, db, it.ID)
	if len(rows) != len(steps) {
		t.Fatalf("driver_usage rows = %d, want one per transition (%d): %+v", len(rows), len(steps), rows)
	}
	// The first row is the engage baseline for this session (no earlier row),
	// so only rows after it carry a measured delta.
	for i, s := range steps {
		r := rows[i]
		if r.Trigger != s.wantTrigger {
			t.Errorf("row %d trigger = %q, want %q", i, r.Trigger, s.wantTrigger)
		}
		if r.SessionID != "sess-du-seam" || r.Executable != agentcli.HarnessClaude {
			t.Errorf("row %d session/executable = %q/%q", i, r.SessionID, r.Executable)
		}
		if i > 0 && (r.FreshInput != s.fresh || r.Output != s.out) {
			t.Errorf("row %d delta fresh=%d out=%d, want %d/%d", i, r.FreshInput, r.Output, s.fresh, s.out)
		}
	}

	// A move whose source is already engaged is a transition, not an engage:
	// only entering an engaged state from outside one is labelled engage.
	if got := driverUsageTrigger(ctx, it, "in_progress", "in_progress"); got != DriverTriggerTransition {
		t.Errorf("engaged → engaged trigger = %q, want %q", got, DriverTriggerTransition)
	}
	if got := driverUsageTrigger(ctx, it, "backlog", "in_progress"); got != DriverTriggerEngage {
		t.Errorf("backlog → in_progress trigger = %q, want %q", got, DriverTriggerEngage)
	}

	// AC7 end to end: re-issuing the terminal story-set changes nothing and
	// writes no second row.
	_, _ = Dispatch(ctx, "story-set", json.RawMessage(`{"id":"`+it.ID+`","status":"done"}`))
	if again := driverUsageRows(t, db, it.ID); len(again) != len(steps) {
		t.Fatalf("re-issued story-set wrote a row: %d rows, want %d", len(again), len(steps))
	}
}
