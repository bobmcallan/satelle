package verb_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestStoryDocAttachListGet(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetStoryDir(filepath.Join(dir, "stories"))
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocIndexStore(nil)
		verb.SetStoryDir("")
	})

	var st workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "Has docs", "acceptance_criteria": "1. ok",
	}), &st)

	// Attach a typed document.
	var attached struct{ StoryID, Name, Type string }
	json.Unmarshal(call(t, "story-doc-attach", map[string]any{
		"story_id": st.ID, "name": "initial-plan", "type": "plan",
		"body": "## Plan\n\nStep one.",
	}), &attached)
	if attached.Name != "initial-plan" || attached.Type != "plan" {
		t.Fatalf("attach returned %+v", attached)
	}

	// List the story's documents.
	var docs []struct{ StoryID, Name, Type string }
	json.Unmarshal(call(t, "story-doc-list", map[string]any{"story_id": st.ID}), &docs)
	if len(docs) != 1 || docs[0].Name != "initial-plan" || docs[0].Type != "plan" {
		t.Fatalf("list returned %+v", docs)
	}

	// Retrieve the document body.
	var got struct{ Name, Type, Body string }
	json.Unmarshal(call(t, "story-doc-get", map[string]any{"story_id": st.ID, "name": "initial-plan"}), &got)
	if !strings.Contains(got.Body, "Step one.") || got.Type != "plan" {
		t.Errorf("get returned %+v", got)
	}

	// The attachment is recorded on the story's ledger (per-story read).
	var entries []map[string]any
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": st.ID, "limit": 100}), &entries)
	var sawAttach bool
	for _, e := range entries {
		if e["kind"] == verb.KindStoryDocAttached {
			sawAttach = true
		}
	}
	if !sawAttach {
		t.Errorf("per-story ledger missing the doc-attached entry; got %d entries", len(entries))
	}
}
