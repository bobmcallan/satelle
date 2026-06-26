package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// openTemp opens a fresh store in a temp dir, registering cleanup.
func openTemp(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".satelle", "satelle.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCreatesDBAndIsReopenable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".satelle", "satelle.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("db file not created: %v", err)
	}
	// Reopening an existing store re-runs migrations idempotently.
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	db2.Close()
}

func TestStoryLifecycle(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	now := time.Now()

	it, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind:  workitem.KindStory,
		Title: "Scaffold satelle",
		Tags:  []string{"mvp"},
	}, now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if it.ID == "" || it.ID[:4] != "sty_" {
		t.Errorf("story id = %q, want sty_ prefix", it.ID)
	}
	if it.Status != workitem.StatusOpen {
		t.Errorf("default status = %q, want open", it.Status)
	}

	got, err := db.Stories.Get(ctx, it.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "Scaffold satelle" || len(got.Tags) != 1 || got.Tags[0] != "mvp" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	upd, err := db.Stories.SetStatus(ctx, it.ID, workitem.StatusDone, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("set status: %v", err)
	}
	if upd.Status != workitem.StatusDone {
		t.Errorf("status = %q, want done", upd.Status)
	}
	if !upd.UpdatedAt.After(upd.CreatedAt) {
		t.Errorf("updated_at not advanced: created=%v updated=%v", upd.CreatedAt, upd.UpdatedAt)
	}

	if _, err := db.Stories.Get(ctx, "sty_missing"); err != workitem.ErrNotFound {
		t.Errorf("missing get err = %v, want ErrNotFound", err)
	}
}

func TestKindPartitioning(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	now := time.Now()

	if _, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "s1"}, now); err != nil {
		t.Fatal(err)
	}
	tsk, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindTask, Title: "t1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if tsk.ID[:4] != "tsk_" {
		t.Errorf("task id = %q, want tsk_ prefix", tsk.ID)
	}

	stories, err := db.Stories.List(ctx, workitem.ListFilter{Kind: workitem.KindStory})
	if err != nil {
		t.Fatal(err)
	}
	if len(stories) != 1 || stories[0].Title != "s1" {
		t.Errorf("story list = %+v, want only s1", stories)
	}
	all, _ := db.Stories.List(ctx, workitem.ListFilter{})
	if len(all) != 2 {
		t.Errorf("unfiltered list len = %d, want 2", len(all))
	}
}

func TestLedgerAppendAndList(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	now := time.Now()

	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: "sty_abc", Kind: ledger.KindStoryCreated, Body: "created",
	}, now); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: "sty_abc", Kind: ledger.KindComment, Body: "note",
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	entries, err := db.Ledger.ListByStory(ctx, "sty_abc", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Kind != ledger.KindStoryCreated || entries[1].Kind != ledger.KindComment {
		t.Errorf("entries not oldest-first: %v, %v", entries[0].Kind, entries[1].Kind)
	}
	if entries[0].ID[:4] != "evt_" {
		t.Errorf("ledger id = %q, want evt_ prefix", entries[0].ID)
	}

	// Filtered by kind.
	comments, _ := db.Ledger.List(ctx, ledger.ListFilter{StoryID: "sty_abc", Kind: ledger.KindComment})
	if len(comments) != 1 {
		t.Errorf("comment-filtered = %d, want 1", len(comments))
	}

	// Unfiltered list is refused.
	if _, err := db.Ledger.List(ctx, ledger.ListFilter{}); err == nil {
		t.Error("unfiltered list should be refused")
	}
}
