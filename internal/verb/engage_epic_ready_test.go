package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

// sty_a476b913: a child of an epic leaves backlog into an engaging status only
// once its epic-parent's readiness round holds. The fixture is the after-children
// route: the epic's readiness edge is raised(backlog) -> ready, and the `fix`
// child's route declares no propose step at all.

// acceptEpicReadiness records an accepted readiness round on the epic's
// from -> to edge, as `story ready` would.
func acceptEpicReadiness(t *testing.T, db *store.DB, epicID, from, to string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"from": from, "to": to, "attempt": "att_test"})
	if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: epicID, Kind: ledger.KindReadinessAccept, Actor: "executor", Payload: payload,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func wantEpicNotReady(t *testing.T, err error, epicID string) {
	t.Helper()
	wantContains(t, err, epicID, "has not been readied", "satelle story ready "+epicID)
}

// wantEpicNotReadyCannotRun is the refusal when `story ready` cannot run on the
// epic's route: it says why instead of sending the caller to the command.
func wantEpicNotReadyCannotRun(t *testing.T, err error, epicID string) {
	t.Helper()
	wantContains(t, err, epicID, "has not been readied", "cannot run")
	if strings.Contains(err.Error(), "run satelle story ready") {
		t.Errorf("the refusal must not send the caller to a command that cannot run: %v", err)
	}
}

// AC1: a child on a route with no propose step is refused its first engaging
// move, the refusal names the epic and why it cannot be readied, and nothing moves.
func TestEngageChildRefusedWhileEpicNotReady(t *testing.T) {
	wireAfterChildren(t, true)
	epic := waveEpic(t)
	c := acChild(t, epic, "fix")

	wantEpicNotReadyCannotRun(t, setStatus(t, c.ID, "plan"), epic.ID)
	if isPerforming(t, c.ID) {
		t.Error("a refused engagement must not perform")
	}
	if got := statusOf(t, c.ID).Status; got != "backlog" {
		t.Errorf("a refused child stays in backlog, got %q", got)
	}
}

// AC1: a child created straight into an engaging status is refused before its
// row exists.
func TestCreateChildIntoEngagingRefusedWhileEpicNotReady(t *testing.T) {
	wireAfterChildren(t, true)
	epic := waveEpic(t)

	_, err := dispatchRaw(t, "story-create", map[string]any{
		"title": "x", "category": "fix", "tags": []string{"epic:w"}, "status": "plan",
	})
	wantEpicNotReadyCannotRun(t, err, epic.ID)
	if got := string(call(t, "story-list", map[string]any{"tag": "epic:w"})); strings.Contains(got, `"category":"fix"`) {
		t.Errorf("a refused create must leave no row: %s", got)
	}
}

// AC2: the same moves are accepted once the epic's readiness round holds.
func TestEngageChildAllowedWhenEpicReady(t *testing.T) {
	db := wireAfterChildren(t, true)
	epic := waveEpic(t)
	c := acChild(t, epic, "fix")
	wantEpicNotReadyCannotRun(t, setStatus(t, c.ID, "plan"), epic.ID)

	acceptEpicReadiness(t, db, epic.ID, "backlog", "ready")
	if err := setStatus(t, c.ID, "plan"); err != nil {
		t.Fatalf("a child of a readied epic must engage: %v", err)
	}
}

// AC2: a create straight into an engaging status is accepted under a readied epic.
func TestCreateChildIntoEngagingAllowedWhenEpicReady(t *testing.T) {
	db := wireAfterChildren(t, true)
	epic := waveEpic(t)
	acceptEpicReadiness(t, db, epic.ID, "backlog", "ready")

	c := mkEpicStory(t, map[string]any{"category": "fix", "tags": []string{"epic:w"}, "status": "plan"})
	if got := statusOf(t, c.ID).Status; got != "plan" {
		t.Errorf("the child is created engaged, got %q", got)
	}
}

// AC2: a story in no epic set and an epic-parent itself are unaffected.
func TestEngageNoEpicStoryAndEpicParentUnaffected(t *testing.T) {
	wireAfterChildren(t, true)
	epic := waveEpic(t)
	_ = acChild(t, epic, "fix") // the epic has a child, and has not been readied

	loner := mkEpicStory(t, map[string]any{"category": "fix"})
	if err := setStatus(t, loner.ID, "plan"); err != nil {
		t.Errorf("a story in no epic is unaffected: %v", err)
	}
	if err := setStatus(t, epic.ID, "ready"); err != nil {
		t.Errorf("an epic-parent is unaffected: %v", err)
	}
}

// AC3: an un-readied child can still be abandoned, and parked.
func TestCancelChildOfUnreadiedEpicAllowed(t *testing.T) {
	wireAfterChildren(t, true)
	epic := waveEpic(t)

	for _, target := range []string{"cancelled", "blocked"} {
		c := acChild(t, epic, "fix")
		if err := setStatus(t, c.ID, target); err != nil {
			t.Errorf("backlog -> %s of an un-readied epic's child must stay open: %v", target, err)
		}
	}
}
