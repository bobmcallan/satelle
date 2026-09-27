package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

// TestStoryCostShowsDriverSectionAndSessionQuery pins sty_81caa41b AC11: a
// driver_usage row on a story surfaces both on `story cost <id>` (its own
// driver section plus that session's reconciliation) and on
// `story cost --session <id>` (every story the session drove).
func TestStoryCostShowsDriverSectionAndSessionQuery(t *testing.T) {
	tempRepo(t)

	out, err := runRoot(t, "story", "create", "--title", "driver cost row", "--status", "in_progress")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"session_id": "sess-cli-driver-1", "executable": "claude", "model": "claude-sonnet-5",
		"fresh_input": 100, "cache_read": 20, "cache_write": 10, "output": 30,
		"cost_usd": 0.05, "available": true, "trigger": "engage", "to": "in_progress",
		"window_key": created.ID + "|sess-cli-driver-1|engage||in_progress",
		"cumulative": map[string]any{"fresh_input": 100, "cache_read": 20, "cache_write": 10, "output": 30, "cost_usd": 0.05},
	})
	if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: created.ID, Kind: ledger.KindDriverUsage, Actor: "executor", Payload: payload,
	}, time.Now()); err != nil {
		db.Close()
		t.Fatalf("append: %v", err)
	}
	db.Close()

	costOut, err := runRoot(t, "story", "cost", created.ID)
	if err != nil {
		t.Fatalf("story cost: %v\n%s", err, costOut)
	}
	for _, want := range []string{"DRIVER SESSION", "sess-cli-driver-1", "claude", "GRAND TOTAL", "reconciliation"} {
		if !strings.Contains(costOut, want) {
			t.Errorf("story cost output missing %q:\n%s", want, costOut)
		}
	}

	sessionOut, err := runRoot(t, "story", "cost", "--session", "sess-cli-driver-1")
	if err != nil {
		t.Fatalf("story cost --session: %v\n%s", err, sessionOut)
	}
	if !strings.Contains(sessionOut, created.ID) {
		t.Errorf("story cost --session output missing driven story %s:\n%s", created.ID, sessionOut)
	}
	if !strings.Contains(sessionOut, "UNATTRIBUTED") {
		t.Errorf("story cost --session output missing UNATTRIBUTED row:\n%s", sessionOut)
	}
}
