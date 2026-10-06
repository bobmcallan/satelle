package verb_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_d6e209aa AC1, at the verb layer: while the authored workflows dir is
// unreadable, a transition, `story route` (even for a story with a stored route
// document), create, amend and restamp all refuse, name the path, name the
// embedded route that would otherwise govern, and record nothing.
func TestUnreadableWorkflowsDirRefusesTheVerbs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetTxRunner(db.InTx)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetLeaseStore(db.Leases)
	verb.SetStoryDir(filepath.Join(dir, "stories"))
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetTxRunner(nil)
		verb.SetDocIndexStore(nil)
		verb.SetLeaseStore(nil)
		verb.SetStoryDir("")
		verb.SetTransitionGater(nil)
	})
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: true, Accept: true, Skill: "intent-review"}})

	wf := filepath.Join(dir, "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, half := range routeWorkflow {
		if err := os.WriteFile(filepath.Join(wf, name+".toml"), []byte(half), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db.DocIndex.SetRoots(map[string]string{"workflows": wf})
	call(t, "doc-sync", map[string]any{"dirs": map[string]string{"workflows": wf}})

	// Engaged while readable, so a stored route document exists.
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "engaged", "category": "feature", "body": "b", "acceptance_criteria": "1. a",
	}), &it)
	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	if _, err := verb.StoryRoute(ctx, it.ID); err != nil {
		t.Fatalf("baseline route: %v", err)
	}
	before, _ := db.Ledger.ListByStory(ctx, it.ID, "")
	storiesBefore, _ := db.Stories.List(ctx, workitem.ListFilter{})

	// Replace the dir with a regular file: unreadable even for root.
	if err := os.RemoveAll(wf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wf, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	wantRefusal := func(surface string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s must be refused while the workflows dir is unreadable", surface)
		}
		if !errors.Is(err, wfgovern.ErrAuthoredProcessUnreadable) {
			t.Errorf("%s: want ErrAuthoredProcessUnreadable, got %v", surface, err)
		}
		for _, want := range []string{
			wf, "embedded default route", "binary-shipped", "gates would not be the repository's gates",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: refusal must carry %q: %v", surface, want, err)
			}
		}
	}

	_, err = dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "done"})
	wantRefusal("transition", err)
	after, _ := db.Ledger.ListByStory(ctx, it.ID, "")
	if len(after) != len(before) {
		t.Errorf("a refused transition recorded ledger rows: %d before, %d after", len(before), len(after))
	}
	if got, _ := db.Stories.Get(ctx, it.ID); got.Status != "in_progress" {
		t.Errorf("a refused transition moved the story to %q", got.Status)
	}

	_, err = verb.StoryRoute(ctx, it.ID)
	wantRefusal("story route (stored route doc)", err)

	// Amend is refused at the verb's own state check, before any gate. Create and
	// restamp reach the engine's resolver and gate, covered by the agentstep test.
	_, err = dispatchRaw(t, "story-amend", map[string]any{"id": it.ID, "title": "corrected", "reason": "wrong"})
	wantRefusal("amend", err)
	if storiesAfter, _ := db.Stories.List(ctx, workitem.ListFilter{}); len(storiesAfter) != len(storiesBefore) {
		t.Errorf("a refusal stored or removed a story: %d before, %d after", len(storiesBefore), len(storiesAfter))
	}
}
