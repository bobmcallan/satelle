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
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// newActivityPushTestApp builds a minimal app.App over a real store with one
// in-flight story seat, for the mid-dispatch push tests (sty_752c4ef2 AC5).
func newActivityPushTestApp(t *testing.T) (*app.App, string) {
	t.Helper()
	repo := t.TempDir()
	_ = os.WriteFile(filepath.Join(repo, ".git"), []byte(""), 0o644)
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	sty, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "Dispatch Story", Body: "goal",
		AcceptanceCriteria: "1. x", Category: "chore", Status: workitem.StatusInProgress,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := db.Leases.Acquire(ctx, sty.ID, "story", "test@local", "in_progress", true); err != nil {
		t.Fatal(err)
	}
	return &app.App{Config: config.Config{}, RepoRoot: repo, Store: db}, sty.ID
}

// TestBuildSeatSnapshotIsSeatOnly (AC5): the mid-dispatch push replaces only
// the mirror's "seat" partition — a long dispatch never re-sends story/task/
// doc/ledger state on every throttled beat.
func TestBuildSeatSnapshotIsSeatOnly(t *testing.T) {
	a, storyID := newActivityPushTestApp(t)
	if err := a.Store.Leases.SetActivityDetail(context.Background(), storyID, lease.ActivityDetail{
		Agent: "coder", Model: "sonnet", Pid: 4242, EventLabel: "tool: Bash", EventAt: time.Now().UTC(), EventCount: 3,
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := buildSeatSnapshot(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Kinds) != 1 || snap.Kinds[0] != "seat" {
		t.Fatalf("Kinds = %v, want [seat]", snap.Kinds)
	}
	if len(snap.Stories) != 0 || len(snap.Tasks) != 0 || len(snap.Docs) != 0 {
		t.Fatalf("seat-only push must carry no other partition: %+v", snap)
	}
	if len(snap.Seats) != 1 {
		t.Fatalf("expected 1 seat row, got %d", len(snap.Seats))
	}
	var row map[string]any
	if err := json.Unmarshal(snap.Seats[0], &row); err != nil {
		t.Fatal(err)
	}
	if row["id"] != storyID {
		t.Errorf("seat row id = %v, want %v", row["id"], storyID)
	}
}

// TestActivityDetailSinkPushesMirrorMidDispatch (AC5): activityDetailSink —
// the exact body app.go wires as the engine's throttled SetActivityDetail
// sink — both persists the dispatch metadata on the lease row AND posts a
// seat snapshot to the local mirror endpoint, with last_event_at advancing
// across successive throttled calls (proving a refresh, not a one-shot stamp
// that goes stale for the rest of a long dispatch).
func TestActivityDetailSinkPushesMirrorMidDispatch(t *testing.T) {
	a, storyID := newActivityPushTestApp(t)

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

	first := time.Now().UTC().Add(-2 * time.Second)
	second := time.Now().UTC()
	activityDetailSink(a.Store.Leases, a, ts.URL, storyID, agentstep.ActivityDetail{
		Agent: "coder", Model: "sonnet", Pid: 4242, EventLabel: "tool: Bash", EventAt: first, EventCount: 1,
	})
	activityDetailSink(a.Store.Leases, a, ts.URL, storyID, agentstep.ActivityDetail{
		Agent: "coder", Model: "sonnet", Pid: 4242, EventLabel: "message", EventAt: second, EventCount: 2,
	})

	// The lease row itself carries the latest detail (independent of the mirror push).
	l, err := a.Store.Leases.Get(context.Background(), storyID)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := lease.EffectiveActivityDetail(l, time.Now().UTC())
	if !ok {
		t.Fatal("EffectiveActivityDetail not ok after activityDetailSink")
	}
	if d.EventCount != 2 || d.EventLabel != "message" || !d.EventAt.Equal(second) {
		t.Fatalf("lease activity detail = %+v, want the second call's values", d)
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
		for _, raw := range snap.Seats {
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
