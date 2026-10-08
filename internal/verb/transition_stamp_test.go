package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// attachingGater attaches a story document from inside the gate window — a row
// the engine and `satelle story attach` stamp with real wall-clock time — then
// lets the transition through after a beat, so the call's start time and the
// moment the transition commits are measurably apart.
type attachingGater struct{ delay time.Duration }

func (g attachingGater) Gate(ctx context.Context, item workitem.Item, to string) (verb.GateDecision, error) {
	if _, _, err := verb.AttachItemDoc(ctx, item, "gate-note", "output", "attached mid-gate"); err != nil {
		return verb.GateDecision{}, err
	}
	time.Sleep(g.delay) // time-subject: timestamp separation, so the call's start and the commit are measurably apart (a slower clock only widens the gap)
	return verb.GateDecision{Gated: false}, nil
}

// TestTransitionRowsStampedAtCommit pins sty_4a31e1ed AC1: the status_transition
// and change_record rows, and the story's updated_at, carry the time the
// transition commits — not the time the call started, which precedes every row
// the gate window writes. A timestamp-ordered reader (the workstate push cursor)
// would otherwise find them behind a document attached during the window.
func TestTransitionRowsStampedAtCommit(t *testing.T) {
	withWiring(t)
	dir := gitRepo(t)
	chdir(t, dir)
	stories := filepath.Join(dir, "stories")
	_ = os.MkdirAll(stories, 0o755)
	wireWithWorkflows(t, changeWF)
	verb.SetStoryDir(stories)
	verb.SetTransitionGater(attachingGater{delay: 5 * time.Millisecond})

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "stamp", "body": "goal", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": []string{"workflow:cr-wf"},
	}), &it)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	if it.Status != "in_progress" {
		t.Fatalf("status=%s", it.Status)
	}

	list := func(kind string) []ledger.Entry {
		t.Helper()
		var es []ledger.Entry
		json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": kind}), &es)
		if len(es) == 0 {
			t.Fatalf("no %s row for %s", kind, it.ID)
		}
		return es
	}
	attached := list(verb.KindStoryDocAttached)
	transition := list(ledger.KindStatusTransition)
	change := list(ledger.KindChangeRecord)

	att := attached[len(attached)-1].CreatedAt
	tr := transition[len(transition)-1]
	cr := change[len(change)-1]
	if tr.CreatedAt.Before(att) {
		t.Errorf("status_transition created_at %s precedes the mid-gate attachment %s", tr.CreatedAt, att)
	}
	if cr.CreatedAt.Before(att) {
		t.Errorf("change_record created_at %s precedes the mid-gate attachment %s", cr.CreatedAt, att)
	}
	if !it.UpdatedAt.Equal(tr.CreatedAt) {
		t.Errorf("story updated_at %s != transition created_at %s", it.UpdatedAt, tr.CreatedAt)
	}
}
