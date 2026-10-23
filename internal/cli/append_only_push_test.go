package cli

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Operational data is pushed append-only (sty_52b93691): an execution is never
// held, and a record another location holds waits alone rather than stopping the
// rest of the push. These tests fake only the hosted server.

const (
	appendOnlyToml = "[sync]\nstories = \"personal\"\nledger = \"personal\"\nexecutions = \"personal\"\nhold_unpushed = false\n\n[hosted]\nproject = \"probe\"\n"
	// holdBackToml leaves hold_unpushed at its default, so held-back
	// classification runs over the batch.
	holdBackToml = "[sync]\nstories = \"personal\"\nledger = \"personal\"\nexecutions = \"personal\"\n\n[hosted]\nproject = \"probe\"\n"
)

func createExecutionID(t *testing.T, title string) string {
	t.Helper()
	db := openScopeDB(t)
	defer func() { _ = db.Close() }()
	it, err := db.Stories.Create(context.Background(), workitem.CreateInput{Kind: workitem.KindExecution, Title: title}, time.Now())
	if err != nil {
		t.Fatalf("create execution %q: %v", title, err)
	}
	return it.ID
}

func (fx *holdFixture) checkedOut(id string) bool {
	for _, c := range fx.hs.checkoutIDs() {
		if strings.HasPrefix(c, id+"@") {
			return true
		}
	}
	return false
}

// AC2: an execution whose own id has a hold in the local registry (held by
// another location) is still pushed, with no skip line and no hold lookup.
func TestPushDeliversAnExecutionWhoseIDCarriesAForeignHold(t *testing.T) {
	fx := newHoldFixture(t, appendOnlyToml)
	exe := createExecutionID(t, "run held elsewhere")
	fx.hs.setHolder(exe, otherLoc, "desk")
	if err := hosted.RecordHold(fx.ts.URL, "probe", fx.repo, exe, hosted.HoldRecord{Location: otherLoc}); err != nil {
		t.Fatal(err)
	}

	out := fx.push(t)

	if !fx.f.hasItem("probe", exe) {
		t.Fatalf("the execution was not delivered:\n%s", out)
	}
	if strings.Contains(out, "skip "+exe) || strings.Contains(out, "held elsewhere") {
		t.Errorf("an execution must not be reported held elsewhere:\n%s", out)
	}
}

// AC2: with held-back classification on, an execution whose id is held here in
// the registry is neither held back nor re-checked-out; it is delivered.
func TestPushNeverHoldsBackOrChecksOutAnExecution(t *testing.T) {
	fx := newHoldFixture(t, holdBackToml)
	exe := createExecutionID(t, "run held here")
	fx.hs.setHolder(exe, fx.loc, "box")
	if err := hosted.RecordHold(fx.ts.URL, "probe", fx.repo, exe, hosted.HoldRecord{Location: fx.loc}); err != nil {
		t.Fatal(err)
	}

	out := fx.push(t)

	if !fx.f.hasItem("probe", exe) {
		t.Fatalf("the execution was not delivered:\n%s", out)
	}
	if fx.checkedOut(exe) {
		t.Errorf("an execution was checked out: %v", fx.hs.checkoutIDs())
	}
	if strings.Contains(out, "held "+exe) {
		t.Errorf("an execution was held back:\n%s", out)
	}
}

// AC2: a pending claim recorded for an execution is cleared at the next push
// without placing a hold.
func TestPushClearsAPendingClaimOnAnExecutionWithoutCheckout(t *testing.T) {
	fx := newHoldFixture(t, appendOnlyToml)
	exe := createExecutionID(t, "run claimed before the change")
	fx.hs.setHolder(exe, otherLoc, "desk")
	if err := hosted.RecordPendingClaim(fx.ts.URL, "probe", fx.repo, exe, time.Now()); err != nil {
		t.Fatal(err)
	}

	fx.push(t)

	if len(fx.pending(t)) != 0 {
		t.Errorf("the pending claim on an execution stays: %v", fx.pending(t))
	}
	if fx.checkedOut(exe) {
		t.Errorf("a pending claim on an execution placed a hold: %v", fx.hs.checkoutIDs())
	}
	if got := fx.hs.holder(exe).LocationID; got != otherLoc {
		t.Errorf("the other holder was disturbed: %q", got)
	}
}

// AC2: the hold verbs refuse an execution id, before any server call.
func TestHoldVerbsRefuseAnExecution(t *testing.T) {
	fx := newHoldFixture(t, appendOnlyToml)
	exe := createExecutionID(t, "run")
	fx.hs.setHolder(exe, otherLoc, "desk")
	for _, verbName := range []string{"checkout", "takeover"} {
		out, err := runRoot(t, "story", "hold", verbName, exe)
		if err == nil {
			t.Fatalf("%s of an execution must be refused:\n%s", verbName, out)
		}
		if !strings.Contains(err.Error(), exe+" is an execution; executions are append-only and are never held") {
			t.Errorf("%s: unclear refusal: %v", verbName, err)
		}
	}
	if fx.checkedOut(exe) || fx.hs.holder(exe).LocationID != otherLoc {
		t.Errorf("a refused hold verb reached the server: %v %+v", fx.hs.checkoutIDs(), fx.hs.holder(exe))
	}
}

// AC3: a record the server refuses as held is skipped on its own and reported by
// id; every other record is delivered and the push does not fail.
func TestPushSkipsARecordTheServerRefusesAsHeld(t *testing.T) {
	fx := newHoldFixture(t, appendOnlyToml)
	exe := createExecutionID(t, "run the server still holds")
	story := createStoryID(t, "unrelated story")
	fx.f.refuseHeld = map[string]bool{exe: true}

	out := fx.push(t)

	if fx.f.hasItem("probe", exe) {
		t.Error("the refused execution was stored")
	}
	if !fx.f.hasItem("probe", story) || fx.f.ledgerCount("probe", story) == 0 {
		t.Errorf("the rest of the push was not delivered:\n%s", out)
	}
	if !strings.Contains(out, "skip "+exe+" (held elsewhere)") {
		t.Errorf("the refused record is not reported by id:\n%s", out)
	}
}

// AC3: a ledger row the server refuses as held is skipped on its own, and the
// ledger cursor stops before its story, so the rows are sent once it is free.
func TestPushSkipsALedgerRowTheServerRefusesAsHeld(t *testing.T) {
	fx := newHoldFixture(t, appendOnlyToml)
	refusedStory := createStoryID(t, "story the old server holds")
	other := createStoryID(t, "unrelated story")
	fx.f.refuseHeld = map[string]bool{refusedStory: true}

	out := fx.push(t)

	// The refusal is of the item: the story's events go by Append, which holds nothing
	// back (sty_9bdf08c4), so they are delivered either way.
	if fx.f.hasItem("probe", refusedStory) {
		t.Errorf("the refused story's item was stored:\n%s", out)
	}
	if !fx.f.hasItem("probe", other) || fx.f.ledgerCount("probe", other) == 0 {
		t.Errorf("the rest of the push was not delivered:\n%s", out)
	}
	if !strings.Contains(out, "skip "+refusedStory+" (held elsewhere)") {
		t.Errorf("the refused story is not reported by id:\n%s", out)
	}

	fx.f.refuseHeld = nil
	fx.push(t)
	if !fx.f.hasItem("probe", refusedStory) || fx.f.ledgerCount("probe", refusedStory) == 0 {
		t.Error("the refused story's rows were not sent once the server stopped refusing")
	}
}

// AC4: a story item held elsewhere does not freeze the items cursor. After a
// push that skips one foreign-held item, the next re-sends only the items at or
// after it.
func TestForeignHeldItemDoesNotFreezeTheItemsCursor(t *testing.T) {
	fx := newHoldFixture(t, appendOnlyToml)
	before := createStoryID(t, "before the held story")
	foreign := createStoryID(t, "held by another location")
	after := createStoryID(t, "after the held story")
	fx.hs.setHolder(foreign, otherLoc, "desk")

	fx.push(t)
	itemsAfterFirst, _ := fx.f.sent("probe")
	if itemsAfterFirst != 2 {
		t.Fatalf("first push sent %d items, want the 2 unheld", itemsAfterFirst)
	}

	out := fx.push(t)
	itemsAfterSecond, _ := fx.f.sent("probe")
	if got := itemsAfterSecond - itemsAfterFirst; got != 1 {
		t.Errorf("second push sent %d items, want only the one after the held story", got)
	}
	posted, _ := postedRows(fx.f)
	if n := slices.Index(posted, before); n < 0 || slices.Index(posted[n+1:], before) >= 0 {
		t.Errorf("the item before the held one was re-sent: %v", posted)
	}
	if slices.Contains(posted, foreign) {
		t.Errorf("the held item was sent: %v", posted)
	}
	if !strings.Contains(out, "skip "+foreign+" (held elsewhere)") || !strings.Contains(out, "satelle story hold takeover "+foreign) {
		t.Errorf("the held item is not reported with its takeover pointer:\n%s", out)
	}
	if !fx.f.hasItem("probe", after) {
		t.Error("the item after the held one was not delivered")
	}
}
