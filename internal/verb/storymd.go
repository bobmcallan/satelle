package verb

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// storyDir is where stories are mirrored as portable markdown, wired once at
// bootstrap (SetStoryDir). Empty disables the mirror (e.g. tests that don't opt
// in), so the SQLite store stays the sole path when no dir is configured.
var storyDir string

// SetStoryDir wires the directory that holds the per-story markdown mirror.
func SetStoryDir(dir string) { storyDir = dir }

// writeStoryFile mirrors a story to <storyDir>/<id>.md after a store mutation.
// Best-effort: the store remains the source of truth, so a filesystem error must
// not fail the mutation. Tasks are not mirrored — this is the story surface.
func writeStoryFile(it workitem.Item) {
	if storyDir == "" || it.Kind != workitem.KindStory {
		return
	}
	if err := os.MkdirAll(storyDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(storyDir, it.ID+".md"), workitem.Marshal(it), 0o644)
}

// SyncStories reconciles the markdown mirror with the store. The store is the
// SOURCE OF TRUTH: it imports a .md file ONLY when the store has no such story
// (a copied-in / restored file in a fresh repo), and never overwrites an
// existing story — otherwise a stale file (e.g. one read mid-transition) could
// silently revert live store state. It then exports any store story that lacks a
// file (the one-time migration of pre-existing DB stories). Files are a portable,
// import-on-restore mirror, not a second write path over the store.
func SyncStories(ctx context.Context) (imported, exported int, err error) {
	store, err := requireWorkItem()
	if err != nil {
		return 0, 0, err
	}
	if storyDir == "" {
		return 0, 0, nil
	}
	entries, _ := os.ReadDir(storyDir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(storyDir, e.Name()))
		if rerr != nil {
			continue
		}
		it, perr := workitem.Parse(data)
		if perr != nil || it.ID == "" || it.Kind != workitem.KindStory {
			continue
		}
		// Create-only: skip a story the store already owns. The store is
		// authoritative for existing stories; importing would risk reverting it.
		if _, gerr := store.Get(ctx, it.ID); gerr == nil {
			continue
		}
		if _, uerr := store.Upsert(ctx, it, it.UpdatedAt); uerr == nil {
			imported++
		}
	}
	items, lerr := store.List(ctx, workitem.ListFilter{Kind: workitem.KindStory})
	if lerr != nil {
		return imported, 0, lerr
	}
	for _, it := range items {
		p := filepath.Join(storyDir, it.ID+".md")
		if _, serr := os.Stat(p); os.IsNotExist(serr) {
			if werr := os.MkdirAll(storyDir, 0o755); werr == nil {
				if os.WriteFile(p, workitem.Marshal(it), 0o644) == nil {
					exported++
				}
			}
		}
	}
	return imported, exported, nil
}
