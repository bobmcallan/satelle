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
		if added, err := storyfold.SeedItem(ctx, db.Ledger, story, time.Now()); err != nil || added {
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
		// The chain already had a transition, so the snapshot leaves status to it.
		var p map[string]any
		_ = json.Unmarshal(snaps[0].Payload, &p)
		if _, has := p["status"]; has {
			t.Errorf("the snapshot carries a status although the chain holds a transition: %v", p)
		}
		db.Close()
	}
}

// A story whose chain holds no transition takes its status from the snapshot; one
// whose row disagrees with its transitions keeps that disagreement visible.
func TestSeedStatusComesFromTheSnapshotOnlyWhenTheChainHasNoTransition(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), ".satelle", "satelle.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "never moved", Status: "in_progress"}, time.Now())
	drifted, _ := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "drifted", Status: "backlog"}, time.Now())
	tr, _ := json.Marshal(map[string]string{"from": "backlog", "to": "done"})
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: drifted.ID, Kind: ledger.KindStatusTransition, Payload: tr}, time.Now()); err != nil {
		t.Fatal(err)
	}
	n, err := storyfold.Seed(ctx, db.SQL(), db.Ledger, time.Now())
	if err != nil || n != 2 {
		t.Fatalf("Seed = %d, %v; want both stories seeded", n, err)
	}
	if n, _ := storyfold.Seed(ctx, db.SQL(), db.Ledger, time.Now()); n != 0 {
		t.Errorf("a second Seed seeded %d", n)
	}
	for id, want := range map[string]string{fresh.ID: "in_progress", drifted.ID: "done"} {
		chain, _ := db.Ledger.StoryChain(ctx, id)
		if st, _ := storyfold.Fold(chain, nil); st.Status != want {
			t.Errorf("%s folds to %q, want %q", id, st.Status, want)
		}
	}
	rows := storyfold.Compare(mustGet(t, db, drifted.ID), foldOf(t, db, drifted.ID))
	if len(rows) != 1 || rows[0].Field != "status" {
		t.Errorf("drift = %+v, want the status disagreement left visible", rows)
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
