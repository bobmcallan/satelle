package cli

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/hosted/syncpb"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/storyfold"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// hostedToml binds the repo explicitly and keeps a story's full history for a day
// after its newest event.
const hostedToml = "[sync]\nstories = \"personal\"\nledger = \"personal\"\nhold_unpushed = false\nproject = \"probe\"\nrecent_window = \"24h\"\n"

// hostedModeRepo is a repo in hosted mode (sty_0dc600e1): the user is signed in to
// the fake server, which is the machine's hosted server, and [sync] project is set.
func hostedModeRepo(t *testing.T) (*httptest.Server, *fakeWorkstateServer, string) {
	t.Helper()
	ts, f := newFakeWorkstateServer(t)
	repo := workstateRepo(t, hostedToml)
	if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
		t.Fatal(err)
	}
	seedCredAs(t, ts.URL, "usr_me")
	return ts, f, repo
}

// cacheStory writes a story this checkout already published: its row and events,
// made at at, in the local store and on the fake server, with its head saved.
func cacheStory(t *testing.T, f *fakeWorkstateServer, server, repo, title, status string, at time.Time, extra ...ledger.Entry) string {
	t.Helper()
	ctx := context.Background()
	var id string
	withRepoStore(t, func(db *store.DB) {
		it, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: title, Status: status}, at)
		if err != nil {
			t.Fatal(err)
		}
		id = it.ID
		events := append([]ledger.Entry{
			{ID: ledger.NewID(), Kind: ledger.KindDefinitionSnapshot, Actor: "satelle", Body: "definition", Payload: storyfold.SnapshotPayloadOf(it, true)},
			{ID: ledger.NewID(), Kind: ledger.KindComment, Actor: "bob", Body: "one"},
			{ID: ledger.NewID(), Kind: ledger.KindComment, Actor: "bob", Body: "two"},
		}, extra...)
		base := ""
		var sent []*syncpb.Event
		for _, e := range events {
			e.StoryID, e.BaseID = id, base
			if e.CreatedAt.IsZero() {
				e.CreatedAt = at
			}
			if _, err := db.Ledger.Upsert(ctx, e, at); err != nil {
				t.Fatal(err)
			}
			sent = append(sent, hostedEvent(t, e))
			base = e.ID
		}
		f.chain.seed("probe", id, sent...)
		if err := hosted.SaveStoryHeads(server, "probe", repo, map[string]string{id: base}); err != nil {
			t.Fatal(err)
		}
	})
	seedHostedStory(f, id, title, status, at)
	return id
}

// hostedEvent is e as Chain/Append carries it.
func hostedEvent(t *testing.T, e ledger.Entry) *syncpb.Event {
	t.Helper()
	raw, err := marshalWorkstateLedger(e)
	if err != nil {
		t.Fatal(err)
	}
	return &syncpb.Event{Id: e.ID, StoryId: e.StoryID, BaseId: e.BaseID, WorkflowVersion: e.WorkflowHash, Payload: raw}
}

// workingOnAt is the working_on event a location writes when it engages a story.
func workingOnAt(t *testing.T, location string) ledger.Entry {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"location": location, "state": "in_progress"})
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Entry{ID: ledger.NewID(), Kind: ledger.KindWorkingOn, Actor: "executor", Body: "working on", Payload: payload}
}

// localEvents is how many events the local store holds for a story.
func localEvents(t *testing.T, id string) int {
	t.Helper()
	var n int
	withRepoStore(t, func(db *store.DB) {
		events, err := db.Ledger.StoryChain(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		n = len(events)
	})
	return n
}

func hasMark(t *testing.T, id, mark string) bool {
	t.Helper()
	var ok bool
	withRepoStore(t, func(db *store.DB) {
		var err error
		if ok, err = db.Ledger.HasMark(context.Background(), id, mark); err != nil {
			t.Fatal(err)
		}
	})
	return ok
}

func hasRow(t *testing.T, id string) bool {
	t.Helper()
	var ok bool
	withRepoStore(t, func(db *store.DB) {
		_, err := db.Stories.Get(context.Background(), id)
		ok = err == nil
	})
	return ok
}

// cacheFixture is the four stories AC1 tells apart.
type cacheFixture struct {
	engaged, recent, old, performing string
}

func seedCacheFixture(t *testing.T, f *fakeWorkstateServer, server, repo string) cacheFixture {
	t.Helper()
	loc, err := hosted.LocationID(repo)
	if err != nil {
		t.Fatal(err)
	}
	longAgo, now := time.Now().Add(-72*time.Hour).UTC(), time.Now().UTC()
	return cacheFixture{
		engaged:    cacheStory(t, f, server, repo, "engaged here", "backlog", longAgo, workingOnAt(t, loc)),
		recent:     cacheStory(t, f, server, repo, "recent", "backlog", now),
		old:        cacheStory(t, f, server, repo, "old", "backlog", longAgo),
		performing: cacheStory(t, f, server, repo, "performing elsewhere", "in_progress", longAgo, workingOnAt(t, "loc_elsewhere")),
	}
}

// sty_0dc600e1 AC1: after a snapshot sync in hosted mode only the story this
// machine engaged and the recent one keep their history; the old story and the one
// whose performing status came from the hosted row (another machine's engagement)
// are kept as rows, marked for an on-demand fetch.
func TestHostedSnapshotHoldsOnlyEngagedAndRecentHistory(t *testing.T) {
	ts, f, repo := hostedModeRepo(t)
	fx := seedCacheFixture(t, f, ts.URL, repo)

	if out, err := runRoot(t, "sync", "workstate", "snapshot"); err != nil {
		t.Fatalf("snapshot: %v\n%s", err, out)
	}
	for _, id := range []string{fx.engaged, fx.recent} {
		if n := localEvents(t, id); n == 0 {
			t.Errorf("%s lost its history", id)
		}
	}
	for _, id := range []string{fx.old, fx.performing} {
		if n := localEvents(t, id); n != 0 {
			t.Errorf("%s kept %d event(s), want its history left on the server", id, n)
		}
		if !hasRow(t, id) {
			t.Errorf("%s lost its row", id)
		}
		if !hasMark(t, id, ledger.MarkHistoryRemote) {
			t.Errorf("%s is not marked for an on-demand fetch", id)
		}
	}
	// sty_0dc600e1 AC3: the snapshot read the hosted chain of the held stories only.
	for _, id := range []string{fx.engaged, fx.recent} {
		if f.chain.readCount(id) == 0 {
			t.Errorf("the snapshot did not read the hosted chain of held story %s", id)
		}
	}
	for _, id := range []string{fx.old, fx.performing} {
		if n := f.chain.readCount(id); n != 0 {
			t.Errorf("the snapshot read the hosted chain of %s %d time(s), want none", id, n)
		}
	}
	if got := len(f.chain.stored("probe", fx.old)); got != 3 {
		t.Errorf("the server holds %d event(s) of the trimmed story, want 3", got)
	}
}
