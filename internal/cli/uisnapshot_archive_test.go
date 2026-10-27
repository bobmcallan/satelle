package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_28665836 AC1: the web story board snapshot omits an archived story.
func TestUISnapshotOmitsArchivedStories(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	now := time.Now().UTC()
	mk := func(title string) workitem.Item {
		it, err := db.Stories.Create(ctx, workitem.CreateInput{
			Kind: workitem.KindStory, Title: title, Category: "chore", Status: workitem.StatusBacklog,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	kept, archived := mk("kept"), mk("archived")
	archivedTrue := true
	if _, err := db.Stories.Update(ctx, archived.ID, workitem.UpdateInput{Archived: &archivedTrue}, now); err != nil {
		t.Fatal(err)
	}

	snap, err := buildUISnapshot(ctx, &app.App{Config: config.Config{}, RepoRoot: t.TempDir(), Store: db})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, raw := range snap.Stories {
		var it workitem.Item
		if err := json.Unmarshal(raw, &it); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, it.ID)
	}
	if len(ids) != 1 || ids[0] != kept.ID {
		t.Errorf("snapshot stories = %v, want only %s", ids, kept.ID)
	}
}
