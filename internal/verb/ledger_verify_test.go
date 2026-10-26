package verb_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/storyfold"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_41f61669 AC3: the status_transition row the commit writes inside its
// transaction carries the governing route's version, and the commit does not
// deadlock resolving it on the single-connection pool.
func TestStatusTransitionRowCarriesWorkflowHash(t *testing.T) {
	db := wire(t)
	ctx := context.Background()

	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "hashed", "status": "backlog"}), &it); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("story-set: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the status transition deadlocked resolving the workflow version")
	}

	rows, err := db.Ledger.ListByStory(ctx, it.ID, ledger.KindStatusTransition)
	if err != nil || len(rows) == 0 {
		t.Fatalf("transition rows = %+v, %v", rows, err)
	}
	docs, err := db.DocIndex.ListIndexed(ctx, db.SQL(), "workflows")
	if err != nil {
		t.Fatal(err)
	}
	want := wfgovern.RouteVersion(docs, it)
	if want == "" {
		t.Fatal("no workflow governs the story; the shipped route should")
	}
	if got := rows[len(rows)-1].WorkflowHash; got != want {
		t.Errorf("transition hash = %q, want %q", got, want)
	}
	if rep, err := db.Ledger.VerifyStory(ctx, it.ID); err != nil || len(rep.Breaks) != 0 {
		t.Errorf("chain = %+v, %v", rep, err)
	}
}

// sty_41f61669 AC6: ledger-verify is a registered, read-only verb that reports
// every chain break of one story or of all of them.
func TestLedgerVerifyVerbReportsBreaksAndModifiesNothing(t *testing.T) {
	db := wire(t)
	ctx := context.Background()

	var a []ledger.Entry
	for i := 0; i < 3; i++ {
		e, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: "sty_a", Kind: ledger.KindComment}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		a = append(a, e)
	}
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: "sty_b", Kind: ledger.KindComment}, time.Now()); err != nil {
		t.Fatal(err)
	}

	verify := func(req map[string]any) ledger.ChainReport {
		t.Helper()
		var rep ledger.ChainReport
		if err := json.Unmarshal(call(t, "ledger-verify", req), &rep); err != nil {
			t.Fatal(err)
		}
		return rep
	}

	if rep := verify(nil); len(rep.Breaks) != 0 || rep.Stories != 2 || rep.Events != 4 {
		t.Fatalf("a sound ledger: %+v", rep)
	}

	if _, err := db.SQL().Exec(`UPDATE evidence SET base_id = 'evt_ghost' WHERE id = ?`, a[1].ID); err != nil {
		t.Fatal(err)
	}
	before, err := db.Ledger.ListByStory(ctx, "sty_a", "")
	if err != nil {
		t.Fatal(err)
	}

	rep := verify(map[string]any{"story_id": "sty_a"})
	if len(rep.Breaks) != 1 || rep.Breaks[0].Kind != ledger.BreakMissingBase || rep.Breaks[0].EventID != a[1].ID {
		t.Fatalf("one story: breaks = %+v, want one missing_base on %s", rep.Breaks, a[1].ID)
	}
	if rep := verify(map[string]any{"story_id": "sty_b"}); len(rep.Breaks) != 0 {
		t.Errorf("the other story is sound: %+v", rep)
	}
	if rep := verify(nil); len(rep.Breaks) != 1 {
		t.Errorf("all stories: breaks = %+v, want the one", rep.Breaks)
	}

	after, err := db.Ledger.ListByStory(ctx, "sty_a", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("verify changed the row count: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if before[i].BaseID != after[i].BaseID || before[i].ID != after[i].ID {
			t.Errorf("verify repaired entry %s: base %q -> %q", before[i].ID, before[i].BaseID, after[i].BaseID)
		}
	}
}

// sty_6f952dd0 AC2: a status transition whose from is not the status its chain had
// reached at its place — an earlier attempt's event linked after the story's done —
// is reported by ledger-verify as a stale_from replay break, and the fold stays done.
func TestLedgerVerifyReportsAStaleTransitionAsABreak(t *testing.T) {
	db := wire(t)
	ctx := context.Background()
	now := time.Now().UTC()
	var stale ledger.Entry
	for i, tr := range [][2]string{{"backlog", "in_progress"}, {"in_progress", "done"}, {"plan", "in_progress"}} {
		payload, _ := json.Marshal(map[string]string{"from": tr[0], "to": tr[1]})
		e, err := db.Ledger.Append(ctx, ledger.AppendInput{
			StoryID: "sty_late", Kind: ledger.KindStatusTransition, Body: tr[0] + " → " + tr[1], Payload: payload,
		}, now.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		stale = e
	}

	var rep verb.LedgerVerifyReport
	if err := json.Unmarshal(call(t, "ledger-verify", map[string]any{"story_id": "sty_late"}), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.ReplayBreaks) != 1 || rep.ReplayBreaks[0].Reason != storyfold.ReplayStaleFrom ||
		rep.ReplayBreaks[0].EventID != stale.ID || rep.ReplayBreaks[0].From != "plan" {
		t.Fatalf("replay_breaks = %+v, want one stale_from on %s", rep.ReplayBreaks, stale.ID)
	}
	if len(rep.Breaks) != 0 {
		t.Errorf("the chain's links are sound, got breaks %+v", rep.Breaks)
	}
	chain, _ := db.Ledger.StoryChain(ctx, "sty_late")
	if st, _ := storyfold.Fold(chain, nil); st.Status != "done" {
		t.Errorf("fold = %q, want done", st.Status)
	}
}
