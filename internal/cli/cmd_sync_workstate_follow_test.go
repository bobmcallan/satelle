package cli

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

// otherMachineEvent is an event another machine appended to id after its head,
// stored on the server and queued on the subscription.
func otherMachineEvent(t *testing.T, f *fakeWorkstateServer, id, body string, at time.Time) ledger.Entry {
	t.Helper()
	stored := f.chain.stored("probe", id)
	e := ledger.Entry{ID: ledger.NewID(), StoryID: id, BaseID: stored[len(stored)-1], Kind: ledger.KindComment, Actor: "alice", Body: body, CreatedAt: at}
	ev := hostedEvent(t, e)
	f.chain.seed("probe", id, ev)
	f.chain.mu.Lock()
	f.chain.feed = append(f.chain.feed, ev)
	f.chain.mu.Unlock()
	return e
}

func snapshotCalls(f *fakeWorkstateServer) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets
}

// sty_0dc600e1 AC3: another machine's change to a story this machine holds reaches
// it through the subscription, resumed from the saved heads, with no snapshot.
func TestFollowAppliesAnotherMachinesEventWithoutAPull(t *testing.T) {
	ts, f, repo := hostedModeRepo(t)
	id := cacheStory(t, f, ts.URL, repo, "followed", "backlog", time.Now().UTC())
	head := storyHeads(t, ts.URL, "probe", repo)[id]
	e := otherMachineEvent(t, f, id, "made on another machine", time.Now().UTC())
	pulls := snapshotCalls(f)

	out, err := runRoot(t, "sync", "workstate", "follow")
	if err != nil {
		t.Fatalf("follow: %v\n%s", err, out)
	}
	if !slices.Contains(localIDs(t, id), e.ID) {
		t.Errorf("the other machine's event %s did not reach the local store", e.ID)
	}
	if n := snapshotCalls(f); n != pulls {
		t.Errorf("follow made %d snapshot call(s), want none", n-pulls)
	}
	if got := storyHeads(t, ts.URL, "probe", repo)[id]; got != e.ID {
		t.Errorf("head = %s, want the followed event %s", got, e.ID)
	}
	f.chain.mu.Lock()
	resume := f.chain.subscribes[0].GetResume()[id]
	f.chain.mu.Unlock()
	if resume != head {
		t.Errorf("the subscription resumed %s from %q, want its saved head %s", id, resume, head)
	}
}

// sty_0dc600e1 AC1: a story the store need not hold is trimmed after the event the
// subscription delivered for it; a story whose history is on the server is
// fetched whole when it changes recently; an event for a story with no local row
// that the server does not hold lands nothing.
func TestFollowHoldsOnlyWhatTheStoreNeeds(t *testing.T) {
	ts, f, repo := hostedModeRepo(t)
	longAgo := time.Now().Add(-72 * time.Hour).UTC()
	remote := cacheStory(t, f, ts.URL, repo, "left on the server", "backlog", longAgo)
	if out, err := runRoot(t, "sync", "workstate", "snapshot"); err != nil {
		t.Fatalf("snapshot: %v\n%s", err, out)
	}
	if !hasMark(t, remote, ledger.MarkHistoryRemote) {
		t.Fatalf("%s was not trimmed by the snapshot", remote)
	}
	old := cacheStory(t, f, ts.URL, repo, "old", "backlog", longAgo)
	otherMachineEvent(t, f, old, "an old event", longAgo)
	otherMachineEvent(t, f, remote, "a recent change", time.Now().UTC())
	stranger := ledger.Entry{ID: ledger.NewID(), StoryID: "sty_stranger", Kind: ledger.KindComment, Actor: "alice", Body: "not ours", CreatedAt: time.Now().UTC()}
	f.chain.mu.Lock()
	f.chain.feed = append(f.chain.feed, hostedEvent(t, stranger))
	f.chain.mu.Unlock()

	out, err := runRoot(t, "sync", "workstate", "follow")
	if err != nil {
		t.Fatalf("follow: %v\n%s", err, out)
	}
	if n := localEvents(t, old); n != 0 || !hasMark(t, old, ledger.MarkHistoryRemote) {
		t.Errorf("the old story kept %d event(s) after its followed event, want it trimmed", n)
	}
	if got, want := localIDs(t, remote), f.chain.stored("probe", remote); !slices.Equal(got, want) || hasMark(t, remote, ledger.MarkHistoryRemote) {
		t.Errorf("the recently changed story holds %v, want its whole hosted chain %v", got, want)
	}
	if localEvents(t, "sty_stranger") != 0 {
		t.Error("an event for a story with no local row was applied")
	}
}

// sty_dd594bb7 AC5: a story another machine created reaches this checkout while
// follow runs. Follow lands the row of a story it has no row for through the pull
// scoped to that story — not a snapshot of the project — and engages nothing.
func TestFollowLandsAStoryCreatedElsewhere(t *testing.T) {
	ts, f, repo := hostedModeRepo(t)
	known := cacheStory(t, f, ts.URL, repo, "known here", "backlog", time.Now().UTC())
	const id = "sty_elsewhere"
	at := time.Now().UTC()
	seedHostedStory(f, id, "created on another machine", "in_progress", at)
	created := ledger.Entry{ID: ledger.NewID(), StoryID: id, Kind: ledger.KindComment, Actor: "alice", Body: "created elsewhere", CreatedAt: at}
	ev := hostedEvent(t, created)
	f.chain.seed("probe", id, ev)
	f.chain.mu.Lock()
	f.chain.feed = append(f.chain.feed, ev)
	f.chain.mu.Unlock()
	before := len(f.snapshotRequests())

	out, err := runRoot(t, "sync", "workstate", "follow")
	if err != nil {
		t.Fatalf("follow: %v\n%s", err, out)
	}
	withRepoStore(t, func(db *store.DB) {
		ctx := context.Background()
		row, err := db.Stories.Get(ctx, id)
		if err != nil {
			t.Fatalf("the story created on another machine has no local row after follow: %v\n%s", err, out)
		}
		if row.Title != "created on another machine" || row.Status != "in_progress" {
			t.Errorf("landed row = %q/%s, want the server's title and status", row.Title, row.Status)
		}
		chain, err := db.Ledger.StoryChain(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range chain {
			if e.Kind == ledger.KindWorkingOn {
				t.Errorf("follow engaged %s on this checkout: %+v", id, e)
			}
		}
	})
	if !strings.Contains(out, "1 stor(y/ies) landed") {
		t.Errorf("the follow summary does not count the landed story:\n%s", out)
	}
	reqs := f.snapshotRequests()[before:]
	if len(reqs) == 0 {
		t.Fatal("follow made no request to bring the story in")
	}
	for _, r := range reqs {
		if !slices.Equal(r.GetStoryIds(), []string{id}) {
			t.Errorf("follow made a snapshot request scoped to %v, want only [%s] — no project snapshot", r.GetStoryIds(), id)
		}
	}
	if localEvents(t, known) == 0 {
		t.Error("the story this checkout already held lost its events")
	}
}

// sty_0dc600e1 AC3: a server without the subscription makes follow exit non-zero,
// so satelled keeps the snapshot pass.
func TestFollowRefusesAServerWithoutTheSubscription(t *testing.T) {
	_, f, _ := hostedModeRepo(t)
	f.chain.mu.Lock()
	f.chain.noSubscribe = true
	f.chain.mu.Unlock()
	out, err := runRoot(t, "sync", "workstate", "follow")
	if err == nil || !strings.Contains(err.Error()+out, "no story chain subscription") {
		t.Fatalf("follow against a server without Subscribe: err = %v\n%s", err, out)
	}
}
