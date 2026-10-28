package verb_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_a42f4add AC5: while `story ready` runs a story's readiness dispatch, the
// story's seat row is effectively in flight with the dispatch's agent detail — what
// the page's running indicator reads — and the round never rewrites an existing
// seat's identity nor removes a row it did not insert, accepted, rejected or
// errored.

// seatRig is newRigWith keeping the store, so a test can read and seed the lease
// the verb layer is wired to.
func seatRig(t *testing.T) (*readyRig, *store.DB) {
	t.Helper()
	withWiring(t)
	db := wireWithWorkflowsStore(t, routeHalves(readyDoneToml, readyStepToml("ready", readyReviewers)))
	verb.SetStoryDir(t.TempDir())
	r := &readyRig{
		d: &roundDispatcher{t: t, calls: map[string]int{}, docName: "plan"},
		g: &roundGater{calls: map[string]int{}},
	}
	r.g.verd = func(workitem.Item, string) verb.GateDecision { return acceptAll() }
	verb.SetExecutorDispatcher(r.d)
	verb.SetTransitionGater(r.g)
	activeRig = r
	return r, db
}

// pageSees is what listSeatsJSON pushes and the page renders an indicator from: the
// seat is effectively in flight AND carries dispatch detail.
func pageSees(t *testing.T, db *store.DB, id string) bool {
	t.Helper()
	l, err := db.Leases.Get(context.Background(), id)
	if err != nil {
		return false
	}
	now := time.Now().UTC()
	if !lease.EffectiveInFlight(l, now) {
		return false
	}
	d, ok := lease.EffectiveActivityDetail(l, now)
	return ok && d.Agent == "planner"
}

func settledOf(t *testing.T, l lease.Lease) {
	t.Helper()
	if l.InFlight || !l.InFlightAt.IsZero() || l.InFlightPid != 0 {
		t.Errorf("seat not settled: %+v", l)
	}
	if l.ActivityLabel != "" || l.ActivityIndex != 0 || l.ActivityTotal != 0 || !l.ActivityAt.IsZero() ||
		l.ActivityAgent != "" || l.ActivityModel != "" || l.ActivityPid != 0 ||
		l.ActivityEventLabel != "" || !l.ActivityEventAt.IsZero() || l.ActivityEventCount != 0 {
		t.Errorf("activity columns not cleared: %+v", l)
	}
}

func sameIdentity(t *testing.T, got, want lease.Lease) {
	t.Helper()
	if got.Owner != want.Owner || got.StorySeat != want.StorySeat || got.SeatKey != want.SeatKey || got.State != want.State {
		t.Errorf("seat identity changed: got owner=%q story_seat=%v seat_key=%q state=%q, want owner=%q story_seat=%v seat_key=%q state=%q",
			got.Owner, got.StorySeat, got.SeatKey, got.State, want.Owner, want.StorySeat, want.SeatKey, want.State)
	}
}

func TestReadinessRoundShowsItsDispatchInFlight(t *testing.T) {
	ctx := context.Background()
	outcomes := []string{"accepted", "rejected", "errors"}
	seeds := []string{"no seat", "settled own seat", "settled seat of another owner", "seat already in flight"}

	for _, seed := range seeds {
		for _, outcome := range outcomes {
			t.Run(seed+"/"+outcome, func(t *testing.T) {
				r, db := seatRig(t)
				a := newStoryOf(t, "feature", "a")

				var before lease.Lease
				seeded := seed != "no seat"
				if seeded {
					owner := lease.ResolveOwner()
					if seed == "settled seat of another owner" {
						owner = "someone-else@elsewhere"
					}
					if _, _, _, err := db.Leases.Acquire(ctx, a.ID, "story", owner, "backlog", true); err != nil {
						t.Fatal(err)
					}
					if seed != "seat already in flight" {
						if err := db.Leases.Confirm(ctx, a.ID, "backlog"); err != nil {
							t.Fatal(err)
						}
					}
					var err error
					if before, err = db.Leases.Get(ctx, a.ID); err != nil {
						t.Fatal(err)
					}
				}

				sawInFlight := false
				r.d.perform = func(it workitem.Item, to string) (verb.DispatchResult, error) {
					if to != "plan" {
						return verb.DispatchResult{}, nil
					}
					if seed != "seat already in flight" {
						// The engine's sink stamps agent/model on whichever row exists.
						if err := db.Leases.SetActivityDetail(ctx, it.ID, lease.ActivityDetail{Agent: "planner", Model: "m", Pid: 7}); err != nil {
							t.Errorf("no seat row to stamp the dispatch on: %v", err)
						}
						sawInFlight = pageSees(t, db, it.ID)
						if l, err := db.Leases.Get(ctx, it.ID); err == nil && seeded && l.StorySeat != before.StorySeat {
							t.Errorf("story_seat flipped during the round")
						}
					}
					switch outcome {
					case "errors":
						return verb.DispatchResult{Dispatched: true, Agent: "performer"}, errors.New("boom")
					default:
						call(t, "story-doc-attach", map[string]any{"story_id": it.ID, "name": "plan", "type": "plan", "body": "the plan"})
						return verb.DispatchResult{Dispatched: true, Agent: "performer", Skill: "plan"}, nil
					}
				}
				if outcome == "rejected" {
					r.g.verd = func(workitem.Item, string) verb.GateDecision { return fourReviewers("no") }
				}

				readyCall(t, a.ID)

				if seed != "seat already in flight" && !sawInFlight {
					t.Errorf("during the dispatch the seat was not effectively in flight with the agent detail")
				}
				after, err := db.Leases.Get(ctx, a.ID)
				switch seed {
				case "no seat":
					if !errors.Is(err, lease.ErrNotFound) {
						t.Errorf("the lease the round inserted must be released, got %+v err=%v", after, err)
					}
				case "seat already in flight":
					if err != nil {
						t.Fatal(err)
					}
					if after.InFlight != before.InFlight || !after.InFlightAt.Equal(before.InFlightAt) || after.InFlightPid != before.InFlightPid {
						t.Errorf("an in-flight seat must be left as it was:\nbefore %+v\nafter  %+v", before, after)
					}
					sameIdentity(t, after, before)
				default:
					if err != nil {
						t.Fatalf("a seat the round did not insert must survive it: %v", err)
					}
					settledOf(t, after)
					sameIdentity(t, after, before)
				}
			})
		}
	}
}

// A settled seat is settled again, with no readiness detail left to leak into a
// later dispatch's same-owner re-flight, and an effectively-in-flight seat is not
// marked at all.
func TestMarkInFlightIfSettledAndInsertIfAbsent(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(t.TempDir() + "/l.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ls := db.Leases
	opts := lease.AcquireOpts{ItemID: "sty_x", Kind: "story", Owner: "o@h", State: "backlog", SeatKey: "sty_x"}

	if marked, err := ls.MarkInFlightIfSettled(ctx, "sty_x", time.Now()); err != nil || marked {
		t.Fatalf("a missing row: marked=%v err=%v, want false/nil", marked, err)
	}
	if ins, err := ls.InsertIfAbsent(ctx, opts); err != nil || !ins {
		t.Fatalf("insert on an empty id: ins=%v err=%v", ins, err)
	}
	inserted, _ := ls.Get(ctx, "sty_x")
	if inserted.StorySeat || !inserted.InFlight || inserted.Owner != "o@h" {
		t.Errorf("inserted row = %+v, want an in-flight non-story-seat row of the caller", inserted)
	}
	other := opts
	other.Owner, other.StorySeat, other.State = "p@h", true, "plan"
	if ins, err := ls.InsertIfAbsent(ctx, other); err != nil || ins {
		t.Fatalf("insert over a row: ins=%v err=%v, want false/nil", ins, err)
	}
	if got, _ := ls.Get(ctx, "sty_x"); got != inserted {
		t.Errorf("insert over a row changed it:\nbefore %+v\nafter  %+v", inserted, got)
	}
	if marked, err := ls.MarkInFlightIfSettled(ctx, "sty_x", time.Now()); err != nil || marked {
		t.Fatalf("an effectively in-flight row: marked=%v err=%v, want false/nil", marked, err)
	}

	if err := ls.Confirm(ctx, "sty_x", "plan"); err != nil {
		t.Fatal(err)
	}
	settled, _ := ls.Get(ctx, "sty_x")
	if marked, err := ls.MarkInFlightIfSettled(ctx, "sty_x", time.Now()); err != nil || !marked {
		t.Fatalf("a settled row: marked=%v err=%v, want true/nil", marked, err)
	}
	got, _ := ls.Get(ctx, "sty_x")
	if !got.InFlight || got.InFlightAt.IsZero() {
		t.Errorf("settled row not flagged in flight: %+v", got)
	}
	got.InFlight, got.InFlightAt, got.InFlightPid = settled.InFlight, settled.InFlightAt, settled.InFlightPid
	if got != settled {
		t.Errorf("marking wrote more than the in-flight columns:\nbefore %+v\nafter  %+v", settled, got)
	}
}
