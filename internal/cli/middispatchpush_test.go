package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// newActivityPushTestApp builds a minimal app.App over a real store with one
// performing story whose transition is open and dispatched to the coder, for the
// mid-dispatch push tests (sty_752c4ef2 AC5). The dispatch was recorded at
// dispatchedAt.
func newActivityPushTestApp(t *testing.T, dispatchedAt time.Time) (*app.App, string) {
	t.Helper()
	repo := t.TempDir()
	_ = os.WriteFile(filepath.Join(repo, ".git"), []byte(""), 0o644)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	writeRoute(t, wfDir, wildcardRouteDone, wildcardRouteStep)
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": wfDir}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	sty, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "Dispatch Story", Body: "goal",
		AcceptanceCriteria: "1. x", Category: "chore", Status: workitem.StatusInProgress,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	start := startTransitionOn(t, db, sty.ID, "done")
	payload, _ := json.Marshal(ledger.TransitionActivityPayload{Start: start, Agent: "coder", Model: "sonnet", Pid: 4242})
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: sty.ID, Kind: ledger.KindTransitionActivity, Actor: "executor",
		Body: "dispatch coder", Payload: payload,
	}, dispatchedAt); err != nil {
		t.Fatal(err)
	}
	return &app.App{Config: config.Config{}, RepoRoot: repo, Store: db}, sty.ID
}

// TestBuildEngagementSnapshotIsEngagementOnly (AC5): the mid-dispatch push
// replaces only the mirror's "engagement" partition — a long dispatch never
// re-sends story/task/doc/ledger state on every throttled beat.
func TestBuildEngagementSnapshotIsEngagementOnly(t *testing.T) {
	a, storyID := newActivityPushTestApp(t, time.Now().UTC())
	snap, err := buildEngagementSnapshot(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Kinds) != 1 || snap.Kinds[0] != "engagement" {
		t.Fatalf("Kinds = %v, want [engagement]", snap.Kinds)
	}
	if len(snap.Stories) != 0 || len(snap.Tasks) != 0 || len(snap.Docs) != 0 {
		t.Fatalf("engagement-only push must carry no other partition: %+v", snap)
	}
	if len(snap.Engagements) != 1 {
		t.Fatalf("expected 1 engagement row, got %d", len(snap.Engagements))
	}
	var row map[string]any
	if err := json.Unmarshal(snap.Engagements[0], &row); err != nil {
		t.Fatal(err)
	}
	if row["id"] != storyID || row["agent"] != "coder" || row["in_flight"] != true {
		t.Errorf("engagement row = %v, want %s in flight dispatched to coder", row, storyID)
	}
}

// TestActivityDetailSinkPushesMirrorMidDispatch (AC5): activityDetailSink —
// the exact body app.go wires as the engine's throttled SetActivityDetail sink —
// keeps the dispatch's latest event AND posts an engagement snapshot to the
// local mirror endpoint, with last_event_at advancing across successive
// throttled calls (proving a refresh, not a one-shot stamp that goes stale for
// the rest of a long dispatch).
func TestActivityDetailSinkPushesMirrorMidDispatch(t *testing.T) {
	a, storyID := newActivityPushTestApp(t, time.Now().UTC().Add(-10*time.Second))

	var mu sync.Mutex
	var received []mirror.Snapshot
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var snap mirror.Snapshot
		if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
			t.Errorf("decode push body: %v", err)
		}
		mu.Lock()
		received = append(received, snap)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	t.Cleanup(func() { liveDispatch.Delete(storyID) })

	first := time.Now().UTC().Add(-2 * time.Second)
	second := time.Now().UTC()
	activityDetailSink(a, ts.URL, storyID, agentstep.ActivityDetail{
		Agent: "coder", Model: "sonnet", Pid: 4242, EventLabel: "tool: Bash", EventAt: first, EventCount: 1,
	})
	activityDetailSink(a, ts.URL, storyID, agentstep.ActivityDetail{
		Agent: "coder", Model: "sonnet", Pid: 4242, EventLabel: "message", EventAt: second, EventCount: 2,
	})

	// The process keeps the latest event (independent of the mirror push).
	d, ok := latestDispatchEvent(storyID, "coder")
	if !ok {
		t.Fatal("latestDispatchEvent not ok after activityDetailSink")
	}
	if d.EventCount != 2 || d.EventLabel != "message" || !d.EventAt.Equal(second) {
		t.Fatalf("latest dispatch event = %+v, want the second call's values", d)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) < 2 {
		t.Fatalf("expected at least 2 mid-dispatch mirror pushes, got %d", len(received))
	}
	rowAt := func(snap mirror.Snapshot) time.Time {
		var row struct {
			LastEventAt time.Time `json:"last_event_at"`
		}
		for _, raw := range snap.Engagements {
			var probe map[string]any
			if err := json.Unmarshal(raw, &probe); err == nil && probe["id"] == storyID {
				_ = json.Unmarshal(raw, &row)
				return row.LastEventAt
			}
		}
		return time.Time{}
	}
	if !rowAt(received[1]).After(rowAt(received[0])) {
		t.Errorf("last_event_at did not advance across pushes: first=%v second=%v", rowAt(received[0]), rowAt(received[1]))
	}
}
