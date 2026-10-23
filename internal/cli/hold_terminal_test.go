package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// A story finished on the hosted copy has no hold (sty_aa9eaaba): a hold another
// location left on it is released without the holder, never recorded or reported,
// and never pins the items cursor. A hold on a story still in flight is untouched.
// The fixture route is backlog → in_progress → done, and done is terminal.

// seedHosted puts a story's hosted copy at status, as an earlier ingest from the
// holder's machine left it.
func (fx *holdFixture) seedHosted(t *testing.T, id, status string) {
	t.Helper()
	at := time.Now().UTC().Add(-time.Hour)
	raw, err := marshalWorkstateItem(workitem.Item{ID: id, Kind: workitem.KindStory, Title: id, Status: status, CreatedAt: at, UpdatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	fx.f.mu.Lock()
	defer fx.f.mu.Unlock()
	if fx.f.itemsByID["probe"] == nil {
		fx.f.itemsByID["probe"] = map[string]any{}
	}
	fx.f.itemsByID["probe"][id] = rec
}

// heldElsewhere has another location hold id on the server and in this repo's
// recorded holds, as an earlier pass left them.
func (fx *holdFixture) heldElsewhere(t *testing.T, id string) {
	t.Helper()
	fx.hs.setHolder(id, otherLoc, "desk")
	rec := hosted.HoldRecord{Location: otherLoc, Label: "desk", LastSeen: otherSeen}
	if err := hosted.RecordHold(fx.ts.URL, "probe", fx.repo, id, rec); err != nil {
		t.Fatal(err)
	}
}

func (fx *holdFixture) recorded(t *testing.T, id string) bool {
	t.Helper()
	reg, err := hosted.LoadHolds(fx.ts.URL, "probe", fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := reg[id]
	return ok
}

func (fx *holdFixture) client() *hosted.Client {
	c := hosted.NewClient(fx.ts.URL, hosted.FileStore{}, fx.ts.Client())
	c.SetLocation(fx.loc)
	return c
}

// finishLocally puts the local copy of id at the route's terminal state.
func finishLocally(t *testing.T, id string) workitem.Item {
	t.Helper()
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	it, err := a.Store.Stories.SetStatus(context.Background(), id, "done", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return it
}

// openWired opens the app and wires the verb layer to its document index, the
// route the fixture authored, as a command run does: judging a story terminal
// reads the governing route, which a direct call outside a command run lacks.
func (fx *holdFixture) openWired(t *testing.T) *app.App {
	t.Helper()
	withVerbWiring(t)
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	dirs := map[string]string{"workflows": filepath.Join(fx.repo, ".satelle", "workflows")}
	if _, err := a.Store.DocIndex.Sync(context.Background(), dirs, time.Now()); err != nil {
		t.Fatal(err)
	}
	verb.SetDocIndexStore(a.Store.DocIndex)
	return a
}

func localStory(t *testing.T, id string) workitem.Item {
	t.Helper()
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	it, err := a.Store.Stories.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func ingested(fx *holdFixture, id string) bool {
	for _, it := range fx.f.lastItems("probe") {
		if m, _ := it.(map[string]any); m["id"] == id {
			return true
		}
	}
	return false
}

// AC1: one push takes over and releases a foreign hold on a hosted-done story,
// forgets it locally and ingests the story.
func TestPushReleasesAForeignHoldOnAHostedDoneStory(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "finished elsewhere")
	finishLocally(t, id)
	fx.seedHosted(t, id, "done")
	fx.heldElsewhere(t, id)

	out := fx.push(t)

	if got := fx.hs.takeoverIDs(); !slices.Equal(got, []string{id + "@" + fx.loc}) {
		t.Fatalf("takeovers = %v, want one for %s", got, id)
	}
	if got := fx.hs.releasedIDs(); !slices.Equal(got, []string{id}) {
		t.Fatalf("releases = %v, want %s", got, id)
	}
	if got := fx.hs.holder(id).LocationID; got != "" {
		t.Fatalf("the server still holds %s for %q", id, got)
	}
	if fx.recorded(t, id) {
		t.Fatalf("%s is still in the hold registry", id)
	}
	if !ingested(fx, id) {
		t.Fatalf("%s was not ingested:\n%s", id, out)
	}
	if strings.Contains(out, "held elsewhere") {
		t.Fatalf("a finished story must not be reported held:\n%s", out)
	}
}

// AC1 + AC6: a story still in flight on the hosted copy keeps its hold — no
// takeover — and the push says so.
func TestPushLeavesAForeignHoldOnAHostedInFlightStory(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "in flight elsewhere")
	fx.seedHosted(t, id, "in_progress")
	fx.heldElsewhere(t, id)

	out := fx.push(t)

	if got := fx.hs.takeoverIDs(); len(got) != 0 {
		t.Fatalf("a takeover was made on an in-flight story: %v", got)
	}
	if got := fx.hs.holder(id).LocationID; got != otherLoc {
		t.Fatalf("holder = %q, want it left with %s", got, otherLoc)
	}
	if !strings.Contains(out, "skip "+id+" (held elsewhere)") {
		t.Fatalf("missing the held-elsewhere skip line:\n%s", out)
	}
	if ingested(fx, id) {
		t.Fatalf("%s was ingested while held elsewhere", id)
	}

	// The session-start advisory still names it.
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if line := storySyncAdvisory(a); !strings.Contains(line, id+" held by remote") {
		t.Fatalf("advisory = %q, want it to name %s", line, id)
	}
}

// AC2: partitionByHold keeps a hosted-done story another location holds, records
// no hold for it and forgets any it had — whether the registry knew it or not.
func TestPartitionByHoldKeepsAHostedTerminalStory(t *testing.T) {
	for name, seeded := range map[string]bool{"empty registry": false, "registry seeded with the hold": true} {
		t.Run(name, func(t *testing.T) {
			fx := newHoldFixture(t, holdNoGit)
			id := createStoryID(t, "hosted done")
			fx.seedHosted(t, id, "done")
			fx.hs.setHolder(id, otherLoc, "desk")
			if seeded {
				fx.heldElsewhere(t, id)
			}
			raw, err := marshalWorkstateItem(localStory(t, id))
			if err != nil {
				t.Fatal(err)
			}
			fx.openWired(t)

			skipped, keep, terminal, err := partitionByHold(context.Background(), fx.client(), fx.ts.URL, "probe", fx.repo, []json.RawMessage{raw})

			if err != nil || len(skipped) != 0 || len(keep) != 1 {
				t.Fatalf("skipped=%d keep=%d err=%v, want the item kept", len(skipped), len(keep), err)
			}
			if !slices.Equal(terminal, []string{id}) {
				t.Fatalf("terminalHeld = %v, want %s", terminal, id)
			}
			if fx.recorded(t, id) {
				t.Fatalf("a hold was recorded for the finished story")
			}
			if len(fx.hs.takeoverIDs()) != 0 {
				t.Fatalf("partitionByHold must not change a hold on the server")
			}
		})
	}
}

// AC2: `story hold show` and the held marks report a hosted-done story unheld
// while the server still says another location holds it.
func TestHoldShowReportsAHostedTerminalStoryUnheld(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	finished := createStoryID(t, "hosted done")
	fx.seedHosted(t, finished, "done")
	fx.hs.setHolder(finished, otherLoc, "desk")
	flying := createStoryID(t, "hosted in flight")
	fx.seedHosted(t, flying, "in_progress")
	fx.hs.setHolder(flying, otherLoc, "desk")

	out, err := runRoot(t, "story", "hold", "show", finished, "--server", fx.ts.URL)
	if err != nil || !strings.Contains(out, finished+" unheld") {
		t.Fatalf("show = %q, %v; want unheld", out, err)
	}
	if got := fx.hs.holder(finished).LocationID; got != otherLoc {
		t.Fatalf("the fake server must still report the foreign hold, got %q", got)
	}
	out, err = runRoot(t, "story", "hold", "show", flying, "--server", fx.ts.URL)
	if err != nil || !strings.Contains(out, "held by location "+otherLoc) {
		t.Fatalf("show = %q, %v; want the in-flight story held", out, err)
	}
}

// AC1 (sync pass): a pass that finds the hold on a hosted-done story whose local
// copy is still open releases it and records nothing; a takeover the server
// refuses is retried next pass and records nothing either.
func TestSyncHoldsReleasesAHostedTerminalHold(t *testing.T) {
	for name, failTakeover := range map[string]bool{"takeover succeeds": false, "takeover fails": true} {
		t.Run(name, func(t *testing.T) {
			fx := newHoldFixture(t, holdNoGit)
			id := createStoryID(t, "open here, done there")
			fx.seedHosted(t, id, "done")
			fx.hs.setHolder(id, otherLoc, "desk")
			fx.hs.failTakeover = failTakeover
			a := fx.openWired(t)
			site := holdSite{client: fx.client(), server: fx.ts.URL, project: "probe", repoRoot: fx.repo}

			found := syncHolds(context.Background(), &strings.Builder{}, a, site, passReport{}, []string{id})

			if len(found) != 0 || fx.recorded(t, id) {
				t.Fatalf("a finished story must not be found or recorded: found=%v", found)
			}
			if got := fx.hs.takeoverIDs(); !slices.Equal(got, []string{id + "@" + fx.loc}) {
				t.Fatalf("takeovers = %v, want one attempt", got)
			}
			want := ""
			if failTakeover {
				want = otherLoc
			}
			if got := fx.hs.holder(id).LocationID; got != want {
				t.Fatalf("holder = %q, want %q", got, want)
			}
		})
	}
}

// AC3: a push whose only obstacle is an old hosted-done hold moves the items
// cursor to the newest pushed item.
func TestPushCursorIsNotPinnedByAHostedDoneHold(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	old := createStoryID(t, "old and done")
	finishLocally(t, old)
	fx.seedHosted(t, old, "done")
	fx.heldElsewhere(t, old)
	createStoryID(t, "newer one")
	newest := localStory(t, createStoryID(t, "newer two"))

	fx.push(t)

	cur, err := hosted.LoadWorkstateCursor(fx.ts.URL, "probe", fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	if !cur.ItemsUpdatedAt.Equal(newest.UpdatedAt) {
		t.Fatalf("items cursor = %v, want the newest item's %v", cur.ItemsUpdatedAt, newest.UpdatedAt)
	}
}

// AC4: 100+ hosted-done stories held elsewhere, with the registry already full
// of them (the observed state): the first push sends them all and advances the
// cursor; the next collects nothing and asks the server about nothing.
func TestPushOfManyHostedDoneHeldStoriesCompletes(t *testing.T) {
	const n = 120
	fx := newHoldFixture(t, holdNoGit)
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	var newest time.Time
	for i := range n {
		it, err := a.Store.Stories.Create(context.Background(), workitem.CreateInput{Kind: workitem.KindStory, Title: "done " + string(rune('a'+i%26)), Status: workitem.StatusBacklog}, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		it, err = a.Store.Stories.SetStatus(context.Background(), it.ID, "done", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		newest = it.UpdatedAt
		fx.seedHosted(t, it.ID, "done")
		fx.heldElsewhere(t, it.ID)
	}
	a.Close()

	fx.push(t)

	if items, _ := fx.f.sent("probe"); items != n {
		t.Fatalf("the first push ingested %d items, want %d", items, n)
	}
	cur, err := hosted.LoadWorkstateCursor(fx.ts.URL, "probe", fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	if !cur.ItemsUpdatedAt.Equal(newest) {
		t.Fatalf("items cursor = %v, want the newest item's %v", cur.ItemsUpdatedAt, newest)
	}

	fx.hs.mu.Lock()
	lookups := fx.hs.itemGets
	fx.hs.mu.Unlock()
	fx.push(t)

	if items, _ := fx.f.sent("probe"); items != n {
		t.Fatalf("the second push sent items: %d total, want still %d", items, n)
	}
	fx.hs.mu.Lock()
	defer fx.hs.mu.Unlock()
	if fx.hs.itemGets != lookups {
		t.Fatalf("the second push made %d per-item hold lookups, want 0", fx.hs.itemGets-lookups)
	}
}
