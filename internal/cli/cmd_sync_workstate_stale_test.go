package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/storyfold"
	"github.com/bobmcallan/satelle/internal/verb"
)

func transitionPayload(from, to string) map[string]any {
	return map[string]any{"from": from, "to": to}
}

func localStatus(t *testing.T, id string) string {
	t.Helper()
	db := openScopeDB(t)
	defer func() { _ = db.Close() }()
	row, err := db.Stories.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return row.Status
}

// sty_6f952dd0 AC1: an earlier attempt's events reach this database after the story
// is done, without a base, and are linked to its tail. The one that carries a
// plan → in_progress does not become the story's status: it folds to done, the row
// stays done, and the chain reports the late events as stale.
func TestPullOfALateEarlierAttemptDoesNotReopenADoneStory(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, holdOffToml)
	at := time.Now().Add(time.Hour).UTC()
	const id = "sty_late01"
	seedHostedFull(f, id, "Shipped", "B", "1. x", "done", []string{"x"}, at)
	seedHostedLedger(f, "led_c", id, ledger.KindStoryCreated, map[string]any{"source": "legacy"}, at)
	seedHostedLedger(f, "led_t1", id, ledger.KindStatusTransition, transitionPayload("backlog", "in_progress"), at.Add(time.Second))
	seedHostedLedger(f, "led_t2", id, ledger.KindStatusTransition, transitionPayload("in_progress", "done"), at.Add(2*time.Second))
	if out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL); err != nil {
		t.Fatalf("first pull: %v\n%s", err, out)
	}
	if got := localStatus(t, id); got != "done" {
		t.Fatalf("status after the first pull = %q, want done", got)
	}

	// The earlier, killed attempt: made before the done, arriving after it.
	seedHostedLedger(f, "led_e1", id, ledger.KindStatusTransition, transitionPayload("backlog", "plan"), at.Add(-time.Hour))
	seedHostedLedger(f, "led_e2", id, ledger.KindStatusTransition, transitionPayload("plan", "in_progress"), at.Add(-30*time.Minute))
	out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL)
	if err != nil {
		t.Fatalf("second pull: %v\n%s", err, out)
	}
	if strings.Contains(out, "the pull moved it") {
		t.Errorf("a done story that stayed done was reported as moved:\n%s", out)
	}

	db := openScopeDB(t)
	defer func() { _ = db.Close() }()
	row, err := db.Stories.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := verb.FoldStory(context.Background(), db.Ledger, row)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != "done" || row.Status != "done" {
		t.Errorf("fold = %q, row = %q; want both done", st.Status, row.Status)
	}
	var late []string
	for _, b := range st.StaleTransitions {
		late = append(late, b.EventID)
	}
	slices.Sort(late)
	if !slices.Equal(late, []string{"led_e1", "led_e2"}) {
		t.Errorf("stale transitions = %v, want the earlier attempt's two events", late)
	}
	if d, _ := verb.StoryDrifts(context.Background(), db.Ledger, row); len(d) != 0 {
		t.Errorf("drift after the pull: %+v", d)
	}
}

// sty_6f952dd0 AC3: a pull that rebuilds a done or cancelled story's row to a
// non-terminal status names the story, both statuses and the event that caused it,
// on a plain pull and on --force alike (UpsertForce replaces the status before the
// rebuild reads the row, so the prior status is read ahead of both).
func TestPullNamesATerminalStoryItMovesToANonTerminalStatus(t *testing.T) {
	for _, tc := range []struct {
		prior string
		force bool
	}{{"done", false}, {"done", true}, {"cancelled", false}, {"cancelled", true}} {
		name := tc.prior + "/plain"
		if tc.force {
			name = tc.prior + "/force"
		}
		t.Run(name, func(t *testing.T) {
			ts, f := newFakeWorkstateServer(t)
			seedCred(t, ts.URL)
			workstateRepo(t, holdOffToml)
			at := time.Now().Add(time.Hour).UTC()
			const id = "sty_move01"
			seedHostedFull(f, id, "Reopened", "B", "1. x", "in_progress", []string{"x"}, at)
			seedHostedLedger(f, "led_c", id, ledger.KindStoryCreated, map[string]any{"source": "legacy"}, at)
			seedHostedLedger(f, "led_t1", id, ledger.KindStatusTransition, transitionPayload("backlog", "in_progress"), at.Add(time.Second))
			if out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL); err != nil {
				t.Fatalf("first pull: %v\n%s", err, out)
			}
			// Close the story locally, as an earlier session left it.
			corruptRow(t, id, "status", tc.prior)

			args := []string{"sync", "workstate", "pull", "--server", ts.URL}
			if tc.force {
				args = append(args, "--force")
			}
			out, err := runRoot(t, args...)
			if err != nil {
				t.Fatalf("pull: %v\n%s", err, out)
			}
			for _, want := range []string{id, "was " + tc.prior, "moved it to in_progress", "event led_t1"} {
				if !strings.Contains(out, want) {
					t.Errorf("output does not name %q:\n%s", want, out)
				}
			}
			if got := localStatus(t, id); got != "in_progress" {
				t.Errorf("status = %q, want the chain's in_progress", got)
			}
		})
	}
}

// sty_6f952dd0 AC4: reconcile reports the sty_41f61669 shape — a chain with a
// terminal transition, the seeded definition_snapshot (status done) and then a stale
// non-terminal transition among an earlier attempt's events, whose row was rebuilt
// to the stale status. It reports the drift and names the stale event and exits
// non-zero; --repair puts the row back to done, and the next reconcile is clean
// (the stale event stays listed, as a fact of the chain) and the story publishes.
func TestReconcileReportsAndRepairsAStaleTransitionAfterDone(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, holdOffToml)
	id := createStoryID(t, "shipped then reopened by a late event")
	now := time.Now().UTC()
	db := openScopeDB(t)
	// The sty_41f61669 shape: the done transition, the seeded definition_snapshot
	// (status done) pulled in after it, then an earlier killed attempt's events,
	// among them its plan → in_progress, linked after the snapshot.
	snap, err := db.Stories.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	snap.Status = "done"
	seq := 0
	appendEv := func(kind, body string, payload json.RawMessage) ledger.Entry {
		seq++
		e, err := db.Ledger.Append(context.Background(), ledger.AppendInput{
			StoryID: id, Kind: kind, Body: body, Payload: payload,
		}, now.Add(time.Duration(seq)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	transition := func(from, to string) ledger.Entry {
		return appendEv(ledger.KindStatusTransition, from+" → "+to, mustJSON(transitionPayload(from, to)))
	}
	note := mustJSON(map[string]string{"note": "earlier attempt"})
	transition("backlog", "in_progress")
	transition("in_progress", "done")
	appendEv(ledger.KindDefinitionSnapshot, "definition snapshot", storyfold.SnapshotPayloadOf(snap, true))
	appendEv(ledger.KindWorkingOn, "earlier attempt", note)
	appendEv(ledger.KindStepSummary, "earlier attempt", note)
	stale := transition("plan", "in_progress")
	appendEv(ledger.KindStepSummary, "earlier attempt", note)
	_ = db.Close()
	corruptRow(t, id, "status", "in_progress")

	out, err := runRoot(t, "story", "reconcile")
	if err == nil {
		t.Fatalf("reconcile must exit non-zero on the drifted row:\n%s", out)
	}
	for _, want := range []string{id, "status", `row="in_progress"`, `chain="done"`, "stale transition " + stale.ID, "plan → in_progress"} {
		if !strings.Contains(out, want) {
			t.Errorf("reconcile output lacks %q:\n%s", want, out)
		}
	}

	if out, err := runRoot(t, "story", "reconcile", "--repair"); err != nil || !strings.Contains(out, "stale transition "+stale.ID) {
		t.Fatalf("reconcile --repair: %v\n%s", err, out)
	}
	if got := localStatus(t, id); got != "done" {
		t.Fatalf("status after repair = %q, want done", got)
	}
	out, err = runRoot(t, "story", "reconcile")
	if err != nil || !strings.Contains(out, "stale transition "+stale.ID) {
		t.Errorf("reconcile after the repair: %v\n%s", err, out)
	}

	// The stale event is no drift: the repaired story is not withheld from a push.
	f.resetPosts()
	out, err = runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err != nil || strings.Contains(out, "withheld") {
		t.Fatalf("push after the repair: %v\n%s", err, out)
	}
	if items, _ := postedRows(f); !slices.Contains(items, id) {
		t.Errorf("the repaired story did not publish: %v", items)
	}
}
