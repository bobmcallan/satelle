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

	// Edit the file and re-sync → the store reflects the file (file → store).
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
	if got.Title != "Mirror edited" {
		t.Errorf("sync did not import the edited title, got %q", got.Title)
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
