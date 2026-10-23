package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
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

// sty_d84be8bc AC5: a story pulled whose chain predates the fold (a story_created
// that names only its source, never transitioned) is seeded from the hosted row:
// the local row keeps the hosted title, body, criteria, tags and status, the chain
// gains one definition_snapshot, and a following push finds no drift.
func TestPullSeedsAPulledLegacyStoryFromTheHostedRow(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, holdOffToml)
	hostedAt := time.Now().Add(time.Hour).UTC()
	seedHostedFull(f, "sty_legacy01", "Hosted legacy", "Hosted body", "1. hosted", "plan", []string{"epic:x", "surface:cli"}, hostedAt)
	seedHostedLedger(f, "led_created01", "sty_legacy01", ledger.KindStoryCreated, map[string]any{"source": "legacy"}, hostedAt)

	if out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	db := openScopeDB(t)
	row, err := db.Stories.Get(context.Background(), "sty_legacy01")
	if err != nil {
		t.Fatal(err)
	}
	snaps, _ := db.Ledger.ListByStory(context.Background(), "sty_legacy01", ledger.KindDefinitionSnapshot)
	_ = db.Close()
	if row.Title != "Hosted legacy" || row.Body != "Hosted body" || row.AcceptanceCriteria != "1. hosted" ||
		row.Status != "plan" || len(row.Tags) != 2 || row.Category != "feature" {
		t.Errorf("row after the pull = %+v, want the hosted row's definition and status kept", row)
	}
	if len(snaps) != 1 {
		t.Fatalf("definition_snapshot events = %d, want 1 seeded from the hosted row", len(snaps))
	}
	if d := localDrift(t); len(d) != 0 {
		t.Errorf("drift after the pull: %+v", d)
	}
	out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err != nil || strings.Contains(out, "withheld") {
		t.Errorf("the push after the pull reported drift: %v\n%s", err, out)
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

// A forced pull lets the hosted copy win over an edit only this machine made: the
// local edit stays in the chain (a ledger row is never rewritten), and an event of
// its own sets the hosted definition back.
func TestForcedPullRecordsTheHostedCopyWinning(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, holdOffToml)
	id := createStoryID(t, "Original title")
	if out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	_ = f
	time.Sleep(10 * time.Millisecond) // time-subject: the local edit must be strictly newer than the pushed copy
	if out, err := runRoot(t, "story", "set", id, "--title", "Local edit"); err != nil {
		t.Fatalf("set: %v\n%s", err, out)
	}
	if out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL, "--force"); err != nil {
		t.Fatalf("forced pull: %v\n%s", err, out)
	}
	db := openScopeDB(t)
	defer func() { _ = db.Close() }()
	row, _ := db.Stories.Get(context.Background(), id)
	if row.Title != "Original title" {
		t.Errorf("title = %q, want the hosted copy's", row.Title)
	}
	rows, _ := db.Ledger.ListByStory(context.Background(), id, ledger.KindStoryUpdated)
	var won bool
	for _, r := range rows {
		var p struct {
			Source string `json:"source"`
		}
		_ = json.Unmarshal(r.Payload, &p)
		won = won || strings.Contains(p.Source, "--force")
	}
	if !won {
		t.Errorf("no story_updated records the hosted copy winning: %+v", rows)
	}
	if d := localDrift(t); len(d) != 0 {
		t.Errorf("drift after the forced pull: %+v", d)
	}
}
