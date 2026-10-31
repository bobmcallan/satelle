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
)

// otherMachineEvent is an event another machine appended to id after its head,
// stored on the server and queued on the subscription.
func otherMachineEvent(t *testing.T, f *fakeWorkstateServer, id, body string, at time.Time) ledger.Entry {
	t.Helper()
	return otherMachineEntry(t, f, ledger.Entry{StoryID: id, Kind: ledger.KindComment, Actor: "alice", Body: body, CreatedAt: at})
}

// otherMachineTransition is a status transition another machine appended to id.
func otherMachineTransition(t *testing.T, f *fakeWorkstateServer, id, from, to string) ledger.Entry {
	t.Helper()
	p, err := json.Marshal(map[string]string{"from": from, "to": to})
	if err != nil {
		t.Fatal(err)
	}
	return otherMachineEntry(t, f, ledger.Entry{StoryID: id, Kind: ledger.KindStatusTransition, Actor: "alice", Body: from + " -> " + to, Payload: p, CreatedAt: time.Now().UTC()})
}

// otherMachineEntry chains e after id's stored head on the server and queues it on
// the subscription.
func otherMachineEntry(t *testing.T, f *fakeWorkstateServer, e ledger.Entry) ledger.Entry {
	t.Helper()
	stored := f.chain.stored("probe", e.StoryID)
	e.ID, e.BaseID = ledger.NewID(), stored[len(stored)-1]
	id := e.StoryID
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

// sty_7c01ca14 AC1: in team scope, an event for a story another location performs
// is applied and kept; one for a non-performing story is trimmed after it is
// applied; a story whose history is on the server is fetched whole when the event
// moves it into a performing state, and left there on any other event; a story
// with no local row is left alone.
func TestFollowHoldsOnlyWhatTheStoreNeeds(t *testing.T) {
	ts, f, repo := hostedTeamRepo(t)
	longAgo := time.Now().Add(-72 * time.Hour).UTC()
	moved := cacheStory(t, f, ts.URL, repo, "moved into work elsewhere", "backlog", longAgo)
	idle := cacheStory(t, f, ts.URL, repo, "left on the server", "backlog", longAgo)
	if out, err := runRoot(t, "sync", "workstate", "snapshot"); err != nil {
		t.Fatalf("snapshot: %v\n%s", err, out)
	}
	for _, id := range []string{moved, idle} {
		if !hasMark(t, id, ledger.MarkHistoryRemote) {
			t.Fatalf("%s was not trimmed by the snapshot", id)
		}
	}
	old := cacheStory(t, f, ts.URL, repo, "old", "backlog", longAgo)
	performing := cacheStory(t, f, ts.URL, repo, "performing elsewhere", "in_progress", longAgo, workingOnAt(t, "loc_elsewhere"))
	otherMachineEvent(t, f, old, "an event on a story nobody performs", time.Now().UTC())
	kept := otherMachineEvent(t, f, performing, "an event on a performing story", time.Now().UTC())
	otherMachineTransition(t, f, moved, "backlog", "in_progress")
	otherMachineEvent(t, f, idle, "a comment on a story nobody performs", time.Now().UTC())
	stranger := ledger.Entry{ID: ledger.NewID(), StoryID: "sty_stranger", Kind: ledger.KindComment, Actor: "alice", Body: "not ours", CreatedAt: time.Now().UTC()}
	f.chain.mu.Lock()
	f.chain.feed = append(f.chain.feed, hostedEvent(t, stranger))
	f.chain.mu.Unlock()

	out, err := runRoot(t, "sync", "workstate", "follow")
	if err != nil {
		t.Fatalf("follow: %v\n%s", err, out)
	}
	if n := localEvents(t, old); n != 0 || !hasMark(t, old, ledger.MarkHistoryRemote) {
		t.Errorf("the non-performing story kept %d event(s) after its followed event, want it trimmed", n)
	}
	if !slices.Contains(localIDs(t, performing), kept.ID) || hasMark(t, performing, ledger.MarkHistoryRemote) {
		t.Errorf("the story performed elsewhere holds %v, want its followed event %s kept", localIDs(t, performing), kept.ID)
	}
	if got, want := localIDs(t, moved), f.chain.stored("probe", moved); !slices.Equal(got, want) || hasMark(t, moved, ledger.MarkHistoryRemote) {
		t.Errorf("the story moved into work holds %v, want its whole hosted chain %v", got, want)
	}
	if n := localEvents(t, idle); n != 0 || !hasMark(t, idle, ledger.MarkHistoryRemote) {
		t.Errorf("the idle remote story holds %d event(s), want its history left on the server", n)
	}
	if localEvents(t, "sty_stranger") != 0 {
		t.Error("an event for a story with no local row was applied")
	}
}

// sty_7c01ca14 AC4: outside team scope the follower never trims: an event for a
// non-performing story is applied and its chain kept, and a story whose history
// was left on the server is fetched whole on its next event.
func TestFollowNeverTrimsOutsideTeamScope(t *testing.T) {
	ts, f, repo := hostedModeRepo(t)
	useScope(t, kindPersonal)
	longAgo := time.Now().Add(-72 * time.Hour).UTC()
	idle := cacheStory(t, f, ts.URL, repo, "idle", "backlog", longAgo)
	remote := cacheStory(t, f, ts.URL, repo, "left remote by an earlier team scope", "backlog", longAgo)
	withRepoStore(t, func(db *store.DB) {
		ctx := context.Background()
		if _, err := db.Ledger.DropStoryHistory(ctx, remote, storyHeads(t, ts.URL, "probe", repo)[remote]); err != nil {
			t.Fatal(err)
		}
		if err := db.Ledger.SetMarks(ctx, ledger.MarkHistoryRemote, []string{remote}); err != nil {
			t.Fatal(err)
		}
	})
	e := otherMachineEvent(t, f, idle, "an event on a story nobody performs", time.Now().UTC())
	otherMachineEvent(t, f, remote, "a comment", time.Now().UTC())

	out, err := runRoot(t, "sync", "workstate", "follow")
	if err != nil {
		t.Fatalf("follow: %v\n%s", err, out)
	}
	if got, want := localIDs(t, idle), f.chain.stored("probe", idle); !slices.Contains(got, e.ID) || !slices.Equal(got, want) {
		t.Errorf("the idle story holds %v, want its whole chain %v kept", got, want)
	}
	if got, want := localIDs(t, remote), f.chain.stored("probe", remote); !slices.Equal(got, want) || hasMark(t, remote, ledger.MarkHistoryRemote) {
		t.Errorf("the remote story holds %v, want its whole hosted chain %v fetched", got, want)
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
