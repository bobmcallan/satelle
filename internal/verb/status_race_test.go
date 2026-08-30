package verb_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_2c71eff6: the dispatched planner runs synchronously AFTER the edge's gates
// accept but BEFORE the status write is enacted, so it is handed — and works
// from — the PRE-transition row. Any write it derives from that snapshot and
// lands late would, unguarded, drag status back to the FROM state while the
// ledger already records the transition. These cases pin the ordering, reproduce
// the clobber, and prove the compare-and-set stops it.

// dispatcherFunc is an ExecutorDispatcher built from a closure, so a case can
// observe (or mutate) the store at the exact moment the named agent performs.
type dispatcherFunc func(context.Context, workitem.Item, string) (verb.DispatchResult, error)

func (f dispatcherFunc) DispatchExecutor(ctx context.Context, it workitem.Item, to string) (verb.DispatchResult, error) {
	return f(ctx, it, to)
}

// engageWithDispatch runs backlog→in_progress with a dispatcher that records the item
// it was handed, returning the story id and the dispatched agent's snapshot.
func engageWithDispatch(t *testing.T, db *store.DB) (string, workitem.Item) {
	t.Helper()
	var seen workitem.Item
	verb.SetExecutorDispatcher(dispatcherFunc(func(_ context.Context, it workitem.Item, _ string) (verb.DispatchResult, error) {
		seen = it
		return verb.DispatchResult{Dispatched: true, Agent: "planner", Command: "fake {system}", Skill: "plan"}, nil
	}))
	t.Cleanup(func() { verb.SetExecutorDispatcher(nil) })

	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "race", "status": "backlog"}), &it); err != nil {
		t.Fatal(err)
	}
	var moved workitem.Item
	if err := json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &moved); err != nil {
		t.Fatal(err)
	}
	if moved.Status != "in_progress" {
		t.Fatalf("transition did not enact: status = %q", moved.Status)
	}
	if seen.ID != it.ID {
		t.Fatalf("dispatcher was not called for %s", it.ID)
	}
	return it.ID, seen
}

// AC1 — the write ordering, asserted: the dispatched planner is handed the row
// as it stood BEFORE the transition, and an unguarded write built on that stale
// snapshot reverts the committed status, leaving the row disagreeing with the
// ledger exactly as `story reconcile` reported in the field.
func TestDispatchedAgentSeesPreTransitionRowAndUnguardedLateWriteReverts(t *testing.T) {
	db := wire(t)
	ctx := context.Background()
	id, stale := engageWithDispatch(t, db)

	if stale.Status != workitem.StatusBacklog {
		t.Fatalf("dispatched agent saw status %q, want the pre-transition backlog — the ordering this story is about", stale.Status)
	}

	// The clobber, reproduced: the agent writes back what it read (its body edit
	// carries the status from its own snapshot) after the transition committed.
	body := "planner output"
	if _, err := db.Stories.Update(ctx, id, workitem.UpdateInput{
		Body: &body, Status: &stale.Status,
	}, time.Now()); err != nil {
		t.Fatalf("unguarded late write: %v", err)
	}
	after, err := db.Stories.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != workitem.StatusBacklog {
		t.Fatalf("expected the unguarded late write to reproduce the revert, got status %q", after.Status)
	}
	drifts, err := verb.DetectStatusDrift(ctx, db.Stories, db.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 1 || drifts[0].ID != id {
		t.Fatalf("reconcile drift after the clobber = %+v, want exactly one for %s", drifts, id)
	}
}

// AC2 + AC3 — the same late write, now carrying the snapshot it was derived from
// as a compare-and-set (what every workItemSet write does after this fix): it
// loses instead of clobbering, the status stays put, and reconcile is clean.
func TestLateAgentWriteCannotRevertTransition(t *testing.T) {
	db := wire(t)
	ctx := context.Background()
	id, stale := engageWithDispatch(t, db)

	body := "planner output"
	_, err := db.Stories.Update(ctx, id, workitem.UpdateInput{
		Body: &body, Status: &stale.Status, ExpectStatus: &stale.Status,
	}, time.Now())
	if !errors.Is(err, workitem.ErrStatusConflict) {
		t.Fatalf("late stale write err = %v, want ErrStatusConflict", err)
	}
	after, err := db.Stories.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "in_progress" {
		t.Fatalf("status = %q after the refused late write, want in_progress", after.Status)
	}
	drifts, err := verb.DetectStatusDrift(ctx, db.Stories, db.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 0 {
		t.Fatalf("reconcile drift after an engaging transition = %+v, want clean", drifts)
	}
}

// AC2 — the same guard on the other side of the window: when the row moves while
// the gate/dispatch runs, the transition's own write is refused rather than
// silently overwriting whatever landed. Nothing is written, and no
// status_transition row is appended (the CAS failure rolls the tx back).
func TestTransitionRefusedWhenStatusMovesUnderDispatch(t *testing.T) {
	db := wire(t)
	ctx := context.Background()

	verb.SetExecutorDispatcher(dispatcherFunc(func(c context.Context, it workitem.Item, _ string) (verb.DispatchResult, error) {
		// The dispatched agent moves the row out from under the caller.
		blocked := "blocked"
		if _, err := db.Stories.Update(c, it.ID, workitem.UpdateInput{Status: &blocked}, time.Now()); err != nil {
			return verb.DispatchResult{}, err
		}
		return verb.DispatchResult{Dispatched: true, Agent: "planner", Command: "fake {system}", Skill: "plan"}, nil
	}))
	t.Cleanup(func() { verb.SetExecutorDispatcher(nil) })

	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "moved", "status": "backlog"}), &it); err != nil {
		t.Fatal(err)
	}
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	if err == nil {
		t.Fatal("expected the transition to be refused after the row moved under it")
	}
	for _, want := range []string{"transition backlog→in_progress refused", "re-read"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to name %q", err, want)
		}
	}
	after, gerr := db.Stories.Get(ctx, it.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if after.Status != "blocked" {
		t.Errorf("status = %q, want the concurrent writer's blocked (the refused transition wrote nothing)", after.Status)
	}
	entries, lerr := db.Ledger.ListByStory(ctx, it.ID, ledger.KindStatusTransition)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(entries) != 0 {
		t.Errorf("status_transition rows after the refused transition: %d, want 0", len(entries))
	}
}
