package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/storyfold"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// trimmedStory is a published old story whose hosted chain moved it to plan under
// a new title, left by a hosted-mode snapshot with no local history.
func trimmedStory(t *testing.T, f *fakeWorkstateServer, server, repo, title string) string {
	t.Helper()
	at := time.Now().Add(-72 * time.Hour).UTC()
	moved := workitem.Item{Kind: workitem.KindStory, Title: title + " (from the chain)", Status: "plan", CreatedAt: at, UpdatedAt: at}
	id := cacheStory(t, f, server, repo, title, "backlog", at, ledger.Entry{
		ID: ledger.NewID(), Kind: ledger.KindDefinitionSnapshot, Actor: "satelle", Body: "definition",
		Payload: storyfold.SnapshotPayloadOf(moved, true),
	})
	return id
}

// sty_0dc600e1 AC2: story get and story route of a story whose history is on the
// server read its hosted chain, apply it, and render the folded story; a second
// open reads nothing.
func TestOpeningAStoryFetchesItsHistory(t *testing.T) {
	ts, f, repo := hostedModeRepo(t)
	got := trimmedStory(t, f, ts.URL, repo, "opened by get")
	routed := trimmedStory(t, f, ts.URL, repo, "opened by route")
	if out, err := runRoot(t, "sync", "workstate", "snapshot"); err != nil {
		t.Fatalf("snapshot: %v\n%s", err, out)
	}
	for _, id := range []string{got, routed} {
		if localEvents(t, id) != 0 || !hasMark(t, id, ledger.MarkHistoryRemote) {
			t.Fatalf("%s was not trimmed by the snapshot", id)
		}
	}

	reads := f.chain.readCount(got)
	out, err := runRoot(t, "story", "get", got)
	if err != nil {
		t.Fatalf("story get: %v\n%s", err, out)
	}
	if n := f.chain.readCount(got); n != reads+1 {
		t.Errorf("story get read the hosted chain %d time(s), want once", n-reads)
	}
	var item struct{ Title, Status string }
	if err := json.Unmarshal([]byte(out), &item); err != nil {
		t.Fatalf("story get output: %v\n%s", err, out)
	}
	if item.Title != "opened by get (from the chain)" || item.Status != "plan" {
		t.Errorf("story get rendered %q in %s, want the fetched history's title in plan", item.Title, item.Status)
	}
	if n := localEvents(t, got); n != len(f.chain.stored("probe", got)) {
		t.Errorf("the local store holds %d event(s), want the server's %d", n, len(f.chain.stored("probe", got)))
	}
	if hasMark(t, got, ledger.MarkHistoryRemote) {
		t.Error("the story is still marked remote after its history was fetched")
	}
	if out, err := runRoot(t, "story", "get", got); err != nil {
		t.Fatalf("second story get: %v\n%s", err, out)
	}
	if n := f.chain.readCount(got); n != reads+1 {
		t.Errorf("a story whose history is held was read again (%d reads)", n-reads)
	}

	out, err = runRoot(t, "story", "route", routed)
	if err != nil {
		t.Fatalf("story route: %v\n%s", err, out)
	}
	if !strings.Contains(out, "currently **plan**") {
		t.Errorf("story route did not render the fetched history's status:\n%s", out)
	}
	if localEvents(t, routed) == 0 || hasMark(t, routed, ledger.MarkHistoryRemote) {
		t.Error("story route did not fetch the history")
	}
}

// sty_0dc600e1 AC2: the fetch verb satelled runs for the web story page fetches the
// stories it names whose history is on the server, and leaves a held story alone.
func TestSyncWorkstateFetchFetchesOnlyRemoteHistory(t *testing.T) {
	ts, f, repo := hostedModeRepo(t)
	remote := trimmedStory(t, f, ts.URL, repo, "remote")
	if out, err := runRoot(t, "sync", "workstate", "snapshot"); err != nil {
		t.Fatalf("snapshot: %v\n%s", err, out)
	}
	held := cacheStory(t, f, ts.URL, repo, "held", "backlog", time.Now().UTC())
	out, err := runRoot(t, "sync", "workstate", "fetch", remote, held)
	if err != nil {
		t.Fatalf("fetch: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Fetched the history of 1 stor") {
		t.Errorf("fetch report:\n%s", out)
	}
	if localEvents(t, remote) == 0 || f.chain.readCount(held) != 0 {
		t.Errorf("fetch must read the remote story only (held read %d time(s))", f.chain.readCount(held))
	}
}
