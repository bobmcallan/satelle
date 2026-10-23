package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/storyfold"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func snapshots(t *testing.T, db *DB, id string) []ledger.Entry {
	t.Helper()
	rows, err := db.Ledger.ListByStory(context.Background(), id, ledger.KindDefinitionSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// sty_d84be8bc AC3: a story whose chain predates the fold gets one
// definition_snapshot, seeded from its row and linked into the chain, so the fold
// of an existing story equals its row; running the seed again appends nothing.
func TestSeedGivesALegacyStoryOneSnapshotAndNoMore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), ".satelle", "satelle.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// A story as an older binary left it: a row, a story_created that names no
	// definition, and a transition — no snapshot.
	story, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "legacy", Body: "the body", AcceptanceCriteria: "1. x",
		Category: "feature", Tags: []string{"epic:e", "surface:cli"}, Status: "plan",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	task, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindTask, Title: "a task"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"source": "old"})
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: story.ID, Kind: ledger.KindStoryCreated, Payload: payload}, time.Now()); err != nil {
		t.Fatal(err)
	}
	tr, _ := json.Marshal(map[string]string{"from": "backlog", "to": "plan"})
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: story.ID, Kind: ledger.KindStatusTransition, Payload: tr}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	for pass := 1; pass <= 2; pass++ { // the second open must append nothing
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		snaps := snapshots(t, db, story.ID)
		if len(snaps) != 1 {
			t.Fatalf("open %d: %d definition_snapshot events, want exactly 1", pass, len(snaps))
		}
		if snaps[0].BaseID == "" {
			t.Errorf("the snapshot is not linked into the chain: %+v", snaps[0])
		}
		if len(snapshots(t, db, task.ID)) != 0 {
			t.Errorf("a task was seeded: its file is its source of truth")
		}
		if added, err := storyfold.SeedItem(ctx, db.Ledger, story, storyfold.StatusFromRow, time.Now()); err != nil || added {
			t.Errorf("SeedItem again = %v, %v; want nothing appended", added, err)
		}
		chain, err := db.Ledger.StoryChain(ctx, story.ID)
		if err != nil {
			t.Fatal(err)
		}
		st, breaks := storyfold.Fold(chain, nil)
		if len(breaks) != 0 {
			t.Errorf("chain breaks after seeding: %+v", breaks)
		}
		row, err := db.Stories.Get(ctx, story.ID)
		if err != nil {
			t.Fatal(err)
		}
		if drifts := storyfold.Compare(row, st); len(drifts) != 0 {
			t.Errorf("fold differs from the row it was seeded from: %+v", drifts)
		}
		if st.Status != "plan" || st.Title != "legacy" || st.Body != "the body" || len(st.Tags) != 2 {
			t.Errorf("fold = %+v", st)
		}
		db.Close()
	}
}

// A legacy chain's transitions can contradict the story's row: sync inserted
// events out of chronological order and the one-time link chained them in
// insertion order, so the last transition on the chain is not where the story is.
// The snapshot is the newest event and carries the row's status and park origin,
// so the fold equals the row anyway.
func TestSeedMakesTheFoldEqualTheRowWhateverTheLegacyTransitionsSay(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), ".satelle", "satelle.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "never moved", Status: "in_progress"}, time.Now())
	// Row done; the chain's last (insertion-order) transition says in_progress.
	outOfOrder, _ := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "out of order", Status: "done"}, time.Now())
	// Row parked with an origin; the chain holds no park at all.
	parked, _ := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "parked", Status: "blocked"}, time.Now())
	park := "plan"
	if _, err := db.Stories.Update(ctx, parked.ID, workitem.UpdateInput{ParkOrigin: &park}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, from, to string }{
		{outOfOrder.ID, "backlog", "done"},
		{outOfOrder.ID, "plan", "in_progress"},
		{parked.ID, "backlog", "plan"},
	} {
		tr, _ := json.Marshal(map[string]string{"from": c.from, "to": c.to})
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: c.id, Kind: ledger.KindStatusTransition, Payload: tr}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	n, err := storyfold.Seed(ctx, db.SQL(), db.Ledger, time.Now())
	if err != nil || n != 3 {
		t.Fatalf("Seed = %d, %v; want all three stories seeded", n, err)
	}
	if n, _ := storyfold.Seed(ctx, db.SQL(), db.Ledger, time.Now()); n != 0 {
		t.Errorf("a second Seed seeded %d", n)
	}
	for _, id := range []string{fresh.ID, outOfOrder.ID, parked.ID} {
		row := mustGet(t, db, id)
		st := foldOf(t, db, id)
		if st.Status != row.Status || st.ParkOrigin != row.ParkOrigin {
			t.Errorf("%s folds to %q/%q, row is %q/%q", id, st.Status, st.ParkOrigin, row.Status, row.ParkOrigin)
		}
		if drifts := storyfold.Compare(row, st); len(drifts) != 0 {
			t.Errorf("%s: fold differs from the row it was seeded from: %+v", id, drifts)
		}
		// Folding with the route's park classifier (reconcile and push do) gives the
		// same answer: the snapshot, not the older transitions, decides park origin.
		chain, _ := db.Ledger.StoryChain(ctx, id)
		withPark, _ := storyfold.Fold(chain, func(s string) bool { return s == "blocked" })
		if drifts := storyfold.Compare(row, withPark); len(drifts) != 0 {
			t.Errorf("%s: fold with a park classifier differs from the row: %+v", id, drifts)
		}
	}
	db.Close()
}

func mustGet(t *testing.T, db *DB, id string) workitem.Item {
	t.Helper()
	it, err := db.Stories.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func foldOf(t *testing.T, db *DB, id string) storyfold.State {
	t.Helper()
	chain, err := db.Ledger.StoryChain(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := storyfold.Fold(chain, nil)
	return st
}
