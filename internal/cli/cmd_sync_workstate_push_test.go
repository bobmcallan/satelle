package cli

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// appliedItemIDs lists, in order, the item ids every Apply the server received carried.
func appliedItemIDs(f *fakeWorkstateServer) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for _, req := range f.applies {
		for _, it := range req.GetItems() {
			var w struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(it.GetRecord(), &w)
			ids = append(ids, w.ID)
		}
	}
	return ids
}

func resetApplies(f *fakeWorkstateServer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applies = nil
}

// appendLocal makes one local event on a story, as a mutating verb would.
func appendLocal(t *testing.T, id, body string) {
	t.Helper()
	withRepoStore(t, func(db *store.DB) {
		if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{StoryID: id, Kind: ledger.KindComment, Actor: "bob", Body: body}, time.Now()); err != nil {
			t.Fatal(err)
		}
	})
}

func localIDs(t *testing.T, id string) []string {
	t.Helper()
	var ids []string
	for _, e := range localChain(t, id) {
		ids = append(ids, e.ID)
	}
	return ids
}

// sty_0dc600e1 AC8: a hosted-mode push sends the row of the story this machine
// authored, and not the row whose updated_at moved with no event nor the one whose
// newer events were pulled from the server.
func TestHostedPushSendsOnlyAuthoredRows(t *testing.T) {
	ts, f, repo := hostedModeRepo(t)
	now := time.Now().UTC()
	longAgo := now.Add(-72 * time.Hour)
	authored := cacheStory(t, f, ts.URL, repo, "authored", "backlog", longAgo)
	bumped := cacheStory(t, f, ts.URL, repo, "bumped", "backlog", longAgo)
	pulled := cacheStory(t, f, ts.URL, repo, "pulled", "backlog", longAgo)

	appendLocal(t, authored, "made here")
	withRepoStore(t, func(db *store.DB) {
		high := "high"
		if _, err := db.Stories.Update(context.Background(), bumped, workitem.UpdateInput{Priority: &high}, now); err != nil {
			t.Fatal(err)
		}
	})
	chain := localChain(t, pulled)
	f.chain.seed("probe", pulled, hostedEvent(t, ledger.Entry{
		ID: ledger.NewID(), StoryID: pulled, BaseID: chain[len(chain)-1].ID,
		Kind: ledger.KindComment, Actor: "alice", Body: "made elsewhere", CreatedAt: now,
	}))
	seedHostedStory(f, pulled, "pulled", "backlog", now)
	if out, err := runRoot(t, "sync", "workstate", "snapshot"); err != nil {
		t.Fatalf("snapshot: %v\n%s", err, out)
	}
	if n := len(localChain(t, pulled)); n != len(chain)+1 {
		t.Fatalf("the pulled story holds %d event(s), want the server's %d", n, len(chain)+1)
	}
	resetApplies(f)

	if out, err := pushRaw(t, ts.URL); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if got := appliedItemIDs(f); !slices.Equal(got, []string{authored}) {
		t.Errorf("Apply carried %v, want only the authored story %s", got, authored)
	}
	if hasMark(t, authored, ledger.MarkItemPending) {
		t.Error("the authored story is still pending after its row was accepted")
	}
}

// sty_0dc600e1 AC8: when the chain append is accepted and the Apply is not, the
// story's row is sent again by the next scoped push.
func TestHostedPushResendsARowWhoseApplyFailed(t *testing.T) {
	ts, f, repo := hostedModeRepo(t)
	id := cacheStory(t, f, ts.URL, repo, "authored", "backlog", time.Now().Add(-72*time.Hour))
	appendLocal(t, id, "made here")
	f.mu.Lock()
	f.failOnPost = 1
	f.mu.Unlock()
	if out, err := pushRaw(t, ts.URL); err == nil {
		t.Fatalf("a failed Apply must fail the push:\n%s", out)
	}
	if got, want := f.chain.stored("probe", id), localIDs(t, id); !slices.Equal(got, want) {
		t.Fatalf("the chain append was not accepted: the server holds %v, the local chain is %v", got, want)
	}
	if !hasMark(t, id, ledger.MarkItemPending) {
		t.Fatal("the story lost its pending mark when its Apply failed")
	}
	f.mu.Lock()
	f.failOnPost = 0
	f.mu.Unlock()
	resetApplies(f)

	if out, err := runRoot(t, "sync", "workstate", "push", id, "--server", ts.URL); err != nil {
		t.Fatalf("scoped push: %v\n%s", err, out)
	}
	if got := appliedItemIDs(f); !slices.Equal(got, []string{id}) {
		t.Errorf("the scoped push's Apply carried %v, want %s", got, id)
	}
	if hasMark(t, id, ledger.MarkItemPending) {
		t.Error("the mark outlived the accepted Apply")
	}
}

// sty_0dc600e1 AC8: a set-field edit is a local append, so it marks the story and
// the push sends its row.
func TestHostedPushSendsASetFieldEdit(t *testing.T) {
	ts, f, repo := hostedModeRepo(t)
	id := cacheStory(t, f, ts.URL, repo, "to rename", "backlog", time.Now().Add(-72*time.Hour))
	if hasMark(t, id, ledger.MarkItemPending) {
		t.Fatal("a story pulled as published starts pending")
	}
	if out, err := runRoot(t, "story", "set", id, "--title", "renamed"); err != nil {
		t.Fatalf("story set: %v\n%s", err, out)
	}
	if !hasMark(t, id, ledger.MarkItemPending) {
		t.Fatal("the edit did not mark the story")
	}
	if out, err := pushRaw(t, ts.URL); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if got := appliedItemIDs(f); !slices.Contains(got, id) {
		t.Errorf("Apply carried %v, want the edited story %s", got, id)
	}
}

// sty_0dc600e1 AC8: a story with local events past its head and no mark (work made
// before the mark existed) is marked by the push and its row and events are sent.
func TestHostedPushBackfillsUnmarkedUnpushedWork(t *testing.T) {
	ts, f, repo := hostedModeRepo(t)
	id := cacheStory(t, f, ts.URL, repo, "pre-upgrade", "backlog", time.Now().Add(-72*time.Hour))
	appendLocal(t, id, "unpushed before the upgrade")
	withRepoStore(t, func(db *store.DB) {
		if err := db.Ledger.ClearMarks(context.Background(), ledger.MarkItemPending, []string{id}); err != nil {
			t.Fatal(err)
		}
	})
	if out, err := pushRaw(t, ts.URL); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if got := appliedItemIDs(f); !slices.Equal(got, []string{id}) {
		t.Errorf("Apply carried %v, want the backfilled story %s", got, id)
	}
	if got, want := f.chain.stored("probe", id), localIDs(t, id); !slices.Equal(got, want) || !f.chain.carries("unpushed before the upgrade") {
		t.Errorf("the server holds %v, want the local chain %v with the unpushed event", got, want)
	}
}
