package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestBuildUISnapshotEmitsAllKinds proves sty_400c022b AC1/AC4: the sender
// emits full docs (mod_time + provenance + source), non-stub ledger events,
// seats, settings, and identity meta.
func TestBuildUISnapshotEmitsAllKinds(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	repo := t.TempDir()
	// Minimal git repo so gitConfigEmail does not fail the suite (may still be empty).
	_ = os.WriteFile(filepath.Join(repo, ".git"), []byte(""), 0o644)

	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	now := time.Now().UTC()
	sty, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "Snap Story", Body: "goal",
		AcceptanceCriteria: "1. x", Category: "chore", Status: workitem.StatusBacklog,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: sty.ID, Kind: ledger.KindStoryCreated, Body: "created",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	// Acquire a seat so seats appear in the snapshot.
	if _, _, _, err := db.Leases.Acquire(ctx, sty.ID, "story", "test@local", "backlog", true); err != nil {
		t.Fatal(err)
	}

	// Seed an authored doc with mod time via the doc index if possible.
	if db.DocIndex != nil {
		// Write a workflow-ish markdown into a data dir and index if API allows.
		// Fallback: insert is via reindex; for unit scope, empty docs still OK —
		// we assert identity/ledger/seats always, and docs when present.
	}

	a := &app.App{
		Config:   config.Config{},
		RepoRoot: repo,
		Store:    db,
	}
	snap, err := buildUISnapshot(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Stories) == 0 {
		t.Fatal("expected stories in snapshot")
	}
	if len(snap.LedgerEvents) == 0 {
		t.Fatal("expected non-stub ledger events")
	}
	var led map[string]any
	if err := json.Unmarshal(snap.LedgerEvents[0], &led); err != nil {
		t.Fatal(err)
	}
	if led["story_id"] != sty.ID {
		t.Errorf("ledger story_id = %v, want %s", led["story_id"], sty.ID)
	}
	if len(snap.Seats) == 0 {
		t.Fatal("expected seats in snapshot")
	}
	var seat map[string]any
	if err := json.Unmarshal(snap.Seats[0], &seat); err != nil {
		t.Fatal(err)
	}
	if seat["id"] != sty.ID {
		t.Errorf("seat id = %v, want %s", seat["id"], sty.ID)
	}
	if len(snap.Identity) == 0 {
		t.Fatal("expected identity meta blob")
	}
	var id map[string]any
	if err := json.Unmarshal(snap.Identity, &id); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"project_name", "repo_root"} {
		if id[k] == nil || id[k] == "" {
			t.Errorf("identity missing %s: %v", k, id)
		}
	}
	if id["repo_root"] != repo {
		t.Errorf("repo_root = %v, want %s", id["repo_root"], repo)
	}
	if len(snap.Settings) == 0 {
		t.Fatal("expected settings blob")
	}
}

// TestBuildUISnapshotDocsCarryModTimeAndProvenance when docs are indexed.
func TestBuildUISnapshotDocsCarryModTimeAndProvenance(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	repo := t.TempDir()
	dataDir := filepath.Join(repo, ".satelle")
	wfDir := filepath.Join(dataDir, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: toy-wf\ntype: workflow\napplies_to: [\"*\"]\n---\n\n# toy\n"
	if err := os.WriteFile(filepath.Join(wfDir, "toy-wf.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if db.DocIndex != nil {
		if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": wfDir}, time.Now().UTC()); err != nil {
			t.Fatalf("doc sync: %v", err)
		}
	}

	a := &app.App{Config: config.Config{}, RepoRoot: repo, Store: db}
	snap, err := buildUISnapshot(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Docs) == 0 {
		t.Fatal("expected indexed workflow docs in snapshot after Sync")
	}
	var doc map[string]any
	if err := json.Unmarshal(snap.Docs[0], &doc); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "mod_time", "provenance", "source"} {
		if doc[k] == nil || doc[k] == "" {
			t.Errorf("doc missing %s: %v", k, doc)
		}
	}
}
