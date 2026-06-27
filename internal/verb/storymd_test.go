package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestStoryMarkdownMirrorAndSync(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	storyDir := filepath.Join(dir, "stories")
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetStoryDir(storyDir)
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocIndexStore(nil)
		verb.SetStoryDir("")
	})
	ctx := context.Background()

	// Create through the verb seam → a markdown file is mirrored.
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "Mirror me", "acceptance_criteria": "1. it works",
	}), &it)
	mdPath := filepath.Join(storyDir, it.ID+".md")
	if _, err := os.Stat(mdPath); err != nil {
		t.Fatalf("create did not write the story markdown: %v", err)
	}

	// Editing an EXISTING story's file must NOT overwrite the store on sync — the
	// store is authoritative; a stale file can't revert live state.
	data, _ := os.ReadFile(mdPath)
	if err := os.WriteFile(mdPath, []byte(strings.Replace(string(data), "Mirror me", "Mirror edited", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := verb.SyncStories(ctx); err != nil {
		t.Fatalf("SyncStories: %v", err)
	}
	got, err := db.Stories.Get(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Mirror me" {
		t.Errorf("sync overwrote the store from a stale file (create-only violated), got %q", got.Title)
	}

	// A copied-in file for a story the store does NOT have is imported (restore /
	// cross-repo portability).
	restore := workitem.Item{
		ID: "sty_restore01", Kind: workitem.KindStory, Title: "Restored",
		Status: "open", AcceptanceCriteria: "1. restored",
	}
	if err := os.WriteFile(filepath.Join(storyDir, restore.ID+".md"), workitem.Marshal(restore), 0o644); err != nil {
		t.Fatal(err)
	}
	if imp, _, err := verb.SyncStories(ctx); err != nil || imp < 1 {
		t.Fatalf("expected the new file to import, got imp=%d err=%v", imp, err)
	}
	if r, err := db.Stories.Get(ctx, restore.ID); err != nil || r.Title != "Restored" {
		t.Errorf("restored story not imported: %+v err=%v", r, err)
	}

	// A story written straight to the store (no file) is exported on sync.
	direct, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "Direct", AcceptanceCriteria: "1. y",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, exp, err := verb.SyncStories(ctx); err != nil || exp < 1 {
		t.Fatalf("expected ≥1 export of the direct story, got exp=%d err=%v", exp, err)
	}
	if _, err := os.Stat(filepath.Join(storyDir, direct.ID+".md")); err != nil {
		t.Errorf("direct store story was not exported to markdown: %v", err)
	}
}
