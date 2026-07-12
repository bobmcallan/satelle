package verb_test

import (
	"encoding/json"
	"os"
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

func TestStoryLessonsListAcrossStories(t *testing.T) {
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

	var a, b workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "A", "acceptance_criteria": "1. a",
	}), &a)
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "B", "acceptance_criteria": "1. b",
	}), &b)

	call(t, "story-doc-attach", map[string]any{
		"story_id": a.ID, "name": "lessons", "type": "lessons",
		"body": "Friction: gate confusion.",
	})
	call(t, "story-doc-attach", map[string]any{
		"story_id": b.ID, "name": "lessons", "type": "lesson",
		"body": "Friction: context contradiction.",
	})
	// non-lessons noise
	call(t, "story-doc-attach", map[string]any{
		"story_id": a.ID, "name": "plan", "type": "plan",
		"body": "not a lesson",
	})

	rawList := call(t, "story-lessons-list", map[string]any{})
	var list []struct {
		StoryID string `json:"story_id"`
		Name    string `json:"name"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal(rawList, &list); err != nil {
		t.Fatalf("unmarshal %s: %v", rawList, err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 lessons across stories, got %s", rawList)
	}
	seen := map[string]bool{}
	for _, d := range list {
		if d.Type != "lessons" && d.Type != "lesson" {
			t.Errorf("unexpected type %q in %s", d.Type, rawList)
		}
		if d.StoryID == "" || d.Name == "" {
			t.Fatalf("empty story_id or name in %s", rawList)
		}
		seen[d.StoryID] = true
		raw, err := os.ReadFile(filepath.Join(dir, "stories", d.StoryID, d.Name+".md"))
		if err != nil {
			t.Fatalf("read lessons file for %+v: %v\nlist=%s", d, err, rawList)
		}
		if strings.Contains(string(raw), "principles:session") {
			t.Error("lessons must not carry principles:session")
		}
	}
	if !seen[a.ID] || !seen[b.ID] {
		t.Errorf("expected both stories in list: %s (a=%s b=%s)", rawList, a.ID, b.ID)
	}
}
