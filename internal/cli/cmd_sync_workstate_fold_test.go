package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// seedHostedFull is seedHostedStory with the definition fields a story carries.
func seedHostedFull(f *fakeWorkstateServer, id, title, body, criteria, status string, tags []string, updated time.Time) {
	seedHostedStory(f, id, title, status, updated)
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.itemsByID["probe"][id].(map[string]any)
	rec["body"], rec["acceptance_criteria"], rec["tags"], rec["category"] = body, criteria, tags, "feature"
}

func localDrift(t *testing.T) []verb.StoryDrift {
	t.Helper()
	db := openScopeDB(t)
	defer func() { _ = db.Close() }()
	drifts, err := verb.DetectDrift(context.Background(), db.Stories, db.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	return drifts
}

// passRow is a story's cached row as the last sync pass left it, read without the
// seeding store.Open does.
func passRow(t *testing.T, id string) workitem.Item {
	t.Helper()
	db, err := store.OpenReadOnly(runtimeDBPath(t))
	if err != nil {
		t.Fatalf("open db read-only: %v", err)
	}
	defer func() { _ = db.Close() }()
	row, err := db.Stories.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// sty_199ad82b AC2: a story pulled whose chain predates the fold (a story_created
// that names only its source, never transitioned) is not seeded by the pull: the
// local row keeps the hosted title, body, criteria, tags and status, and the
// chain holds the hosted events and nothing the pull made.
func TestPullSeedsNothingOntoAPulledLegacyStory(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, holdOffToml)
	hostedAt := time.Now().Add(time.Hour).UTC()
	seedHostedFull(f, "sty_legacy01", "Hosted legacy", "Hosted body", "1. hosted", "plan", []string{"epic:x", "surface:cli"}, hostedAt)
	seedHostedLedger(f, "led_created01", "sty_legacy01", ledger.KindStoryCreated, map[string]any{"source": "legacy"}, hostedAt)

	if out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	row := passRow(t, "sty_legacy01")
	if row.Title != "Hosted legacy" || row.Body != "Hosted body" || row.AcceptanceCriteria != "1. hosted" ||
		row.Status != "plan" || len(row.Tags) != 2 || row.Category != "feature" {
		t.Errorf("row after the pull = %+v, want the hosted row's definition and status kept", row)
	}
	if got := idsOf(passChain(t, "sty_legacy01")); !slices.Equal(got, []string{"led_created01"}) {
		t.Errorf("chain after the pull = %v, want only the hosted led_created01", got)
	}
}

// sty_d84be8bc AC5: when the hosted row's status disagrees with the chain pulled
// beside it, the local row equals the fold of the chain.
func TestPullRebuildsTheRowFromThePulledChain(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, holdOffToml)
	at := time.Now().Add(time.Hour).UTC()
	seedHostedFull(f, "sty_stale01", "Stale row", "B", "1. x", "done", []string{"x"}, at)
	seedHostedLedger(f, "led_c", "sty_stale01", ledger.KindStoryCreated, map[string]any{"source": "legacy"}, at)
	seedHostedLedger(f, "led_t1", "sty_stale01", ledger.KindStatusTransition, map[string]any{"from": "backlog", "to": "in_progress"}, at.Add(time.Second))

	if out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	db := openScopeDB(t)
	row, err := db.Stories.Get(context.Background(), "sty_stale01")
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "in_progress" {
		t.Errorf("status = %q, want the chain's in_progress, not the hosted row's done", row.Status)
	}
	if d := localDrift(t); len(d) != 0 {
		t.Errorf("drift after the pull: %+v", d)
	}
}

// sty_199ad82b AC1: a forced pull lets the hosted copy win on the row over an edit
// only this machine made, and makes no event to set it back: the chain is left as
// it was and the disagreement is recorded in the sync state.
func TestForcedPullLetsTheHostedCopyWinOnTheRowAndRecordsTheConflict(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	repo := workstateRepo(t, holdOffToml)
	id := createStoryID(t, "Original title")
	if out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	_ = f
	time.Sleep(10 * time.Millisecond) // time-subject: the local edit must be strictly newer than the pushed copy
	if out, err := runRoot(t, "story", "set", id, "--title", "Local edit"); err != nil {
		t.Fatalf("set: %v\n%s", err, out)
	}
	before := passChain(t, id)
	if out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL, "--force"); err != nil {
		t.Fatalf("forced pull: %v\n%s", err, out)
	}
	if row := passRow(t, id); row.Title != "Original title" {
		t.Errorf("title = %q, want the hosted copy's", row.Title)
	}
	after := passChain(t, id)
	sameEvents(t, "the forced pull", before, after)
	for _, r := range after {
		var p struct {
			Source string `json:"source"`
		}
		_ = json.Unmarshal(r.Payload, &p)
		if r.Kind == ledger.KindStoryUpdated && strings.Contains(p.Source, "--force") {
			t.Errorf("the forced pull made %s to set the hosted copy back", r.ID)
		}
	}
	if c := storyConflicts(t, repo)[id]; c.Reason != conflictForcedHostedWins {
		t.Errorf("conflict = %+v, want %q recorded", c, conflictForcedHostedWins)
	}
}
