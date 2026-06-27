package verb_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// wire opens a temp store and wires it into the verb registry, resetting on
// cleanup so cases don't leak globals into each other.
func wire(t *testing.T) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetDocIndexStore(db.DocIndex)
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocIndexStore(nil)
	})
}

func call(t *testing.T, name string, req any) json.RawMessage {
	t.Helper()
	var body json.RawMessage
	if req != nil {
		b, _ := json.Marshal(req)
		body = b
	}
	resp, err := verb.Dispatch(context.Background(), name, body)
	if err != nil {
		t.Fatalf("dispatch %s: %v", name, err)
	}
	return resp
}

func TestVersionVerbNeedsNoStore(t *testing.T) {
	resp, err := verb.Dispatch(context.Background(), "version", nil)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	var info verb.VersionInfo
	if err := json.Unmarshal(resp, &info); err != nil {
		t.Fatal(err)
	}
	if info.Version == "" {
		t.Error("empty version")
	}
}

func TestStoreNotConfigured(t *testing.T) {
	verb.SetWorkItemStore(nil)
	if _, err := verb.Dispatch(context.Background(), "story-list", nil); err == nil {
		t.Error("expected ErrStoreNotConfigured when unwired")
	}
}

func TestStoryCreateGetSetThroughVerbs(t *testing.T) {
	wire(t)

	var created workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "T1", "priority": "high"}), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID[:4] != "sty_" || created.Status != workitem.StatusBacklog {
		t.Fatalf("bad created item: %+v", created)
	}

	var got workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": created.ID}), &got)
	if got.Title != "T1" {
		t.Errorf("get title = %q", got.Title)
	}

	var updated workitem.Item
	json.Unmarshal(call(t, "story-set", map[string]any{"id": created.ID, "status": "done"}), &updated)
	if updated.Status != "done" {
		t.Errorf("set status = %q", updated.Status)
	}

	// Create auto-recorded a ledger event for the story.
	var entries []map[string]any
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": created.ID}), &entries)
	if len(entries) < 1 {
		t.Errorf("expected ledger entries for story, got %d", len(entries))
	}
}

func TestTaskKindIsolation(t *testing.T) {
	wire(t)
	call(t, "story-create", map[string]any{"title": "s"})
	call(t, "task-create", map[string]any{"title": "t"})

	var stories []workitem.Item
	json.Unmarshal(call(t, "story-list", nil), &stories)
	if len(stories) != 1 || stories[0].Kind != workitem.KindStory {
		t.Errorf("story-list returned %d items, kinds wrong: %+v", len(stories), stories)
	}
	var tasks []workitem.Item
	json.Unmarshal(call(t, "task-list", nil), &tasks)
	if len(tasks) != 1 || tasks[0].ID[:4] != "tsk_" {
		t.Errorf("task-list wrong: %+v", tasks)
	}
}

func TestUnknownVerb(t *testing.T) {
	if _, err := verb.Dispatch(context.Background(), "nope-nope", nil); err == nil {
		t.Error("expected error for unknown verb")
	}
}
