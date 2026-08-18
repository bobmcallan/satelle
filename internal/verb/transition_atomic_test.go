package verb_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestTransitionRefusesUnwiredTxRunner(t *testing.T) {
	db := wire(t)
	verb.SetTxRunner(nil)
	t.Cleanup(func() { verb.SetTxRunner(db.InTx) })

	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "no-runner"}), &it); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"id": it.ID, "status": "in_progress"})
	_, err := verb.Dispatch(context.Background(), "story-set", body)
	if err == nil {
		t.Fatal("expected refuse when ledger is wired without a tx runner")
	}
	if want := "without a tx runner"; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want it to name %q", err, want)
	}
	got, gerr := db.Stories.Get(context.Background(), it.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if got.Status != workitem.StatusBacklog {
		t.Errorf("refused transition mutated status to %q", got.Status)
	}
}

func TestTransitionAppendFailureRollsBackRow(t *testing.T) {
	db := wire(t)
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "atomic"}), &it); err != nil {
		t.Fatal(err)
	}
	db.Ledger.InjectAppendError(errors.New("forced append fail"))
	body, _ := json.Marshal(map[string]any{"id": it.ID, "status": "in_progress"})
	_, err := verb.Dispatch(context.Background(), "story-set", body)
	if err == nil {
		t.Fatal("expected append failure to refuse the transition")
	}
	if !strings.Contains(err.Error(), "forced append fail") {
		t.Errorf("err = %v, want forced append fail", err)
	}
	got, gerr := db.Stories.Get(context.Background(), it.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if got.Status != workitem.StatusBacklog {
		t.Errorf("row status = %q after failed append, want backlog", got.Status)
	}
	entries, lerr := db.Ledger.ListByStory(context.Background(), it.ID, ledger.KindStatusTransition)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(entries) != 0 {
		t.Errorf("status_transition rows after rollback: %d", len(entries))
	}
}
