package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// inFlightSeat opens a seat mid-transition and leaves it there: the dispatch
// window in which the story's committed status is still the source status.
func inFlightSeat(t *testing.T, db *store.DB, it workitem.Item, target, worktree, session string) {
	t.Helper()
	claimEpicSeat(t, db, it, target, worktree, session)
}

// settledEpicSeat gives a sibling a committed seat co-held under the epic's key.
func settledEpicSeat(t *testing.T, db *store.DB, it workitem.Item, status, worktree, session string) {
	t.Helper()
	claimEpicSeat(t, db, it, status, worktree, session)
	if err := db.Leases.Confirm(context.Background(), it.ID, status); err != nil {
		t.Fatal(err)
	}
}

// claimEpicSeat acquires a seat under one shared arbitration key, the way epic
// seat mode co-holds several siblings in distinct working trees. It opens
// in flight; the caller settles it or leaves it there.
func claimEpicSeat(t *testing.T, db *store.DB, it workitem.Item, state, worktree, session string) {
	t.Helper()
	l, out, _, err := db.Leases.AcquireWith(context.Background(), lease.AcquireOpts{
		ItemID: it.ID, Kind: "story", Owner: "alice", State: state, SeatKey: "epic:shared",
		StorySeat: true, Worktree: worktree, SessionID: session,
	})
	if err != nil || out != lease.OutcomeAcquired {
		t.Fatalf("acquire %s: outcome=%v err=%v lease=%+v", it.ID, out, err, l)
	}
}

func setDispatch(t *testing.T, agent, step, item string) {
	t.Helper()
	t.Setenv(config.DispatchAgentEnv, agent)
	t.Setenv(config.DispatchStepEnv, step)
	t.Setenv(config.DispatchItemEnv, item)
}

// TestDispatchedPerformerAttributedToItsOwnLease (sty_8d7d1c45 AC1/AC2): an epic
// still committed at `ready` with its own transition to `in_progress` in flight
// is dispatched a coder. The performer is attributed to its own in-flight lease
// and may edit — whether the epic seat is alone or shares the store with sibling
// seats, whichever of them was acquired first (List orders by seat_key, then
// acquired_at, so acquisition order IS store order), and whatever session id the
// performer presents against the one the seat was stamped with.
func TestDispatchedPerformerAttributedToItsOwnLease(t *testing.T) {
	for _, tc := range []struct {
		name      string
		siblings  int    // committed sibling seats co-held under the epic key
		epicFirst bool   // the epic's seat is acquired before the siblings'
		leaseSID  string // session the seats were stamped with
		perfSID   string // session the performer's hook presents
		seatless  bool   // a performing sibling that holds no seat at all
	}{
		// The reported failure: the epic seat alone, the performer's own session
		// differs from the one the driver stamped on the seat.
		{"epic alone, performer session differs from the seat's", 0, true, "sess-driver", "sess-performer", false},
		{"epic alone, performer unstamped, seat stamped", 0, true, "sess-driver", "", false},
		{"epic acquired first, performer session differs", 2, true, "sess-driver", "sess-performer", false},
		{"epic acquired last, performer session differs", 2, false, "sess-driver", "sess-performer", false},
		{"epic acquired first, session shared", 2, true, "sess-driver", "sess-driver", false},
		{"epic acquired last, session shared", 2, false, "sess-driver", "sess-driver", false},
		{"unstamped, ambiguous by a seatless sibling", 1, false, "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, create := attributionRepo(t)
			stubWorktree(t, "/w/driver")
			epic := create("epic", "ready")
			var kids []workitem.Item
			for i := 0; i < tc.siblings; i++ {
				kids = append(kids, create("kid", "in_progress"))
			}
			if tc.seatless {
				create("seatless kid", "in_progress")
			}
			seatKids := func() {
				for i, k := range kids {
					settledEpicSeat(t, db, k, "in_progress", fmt.Sprintf("/w/kid%d", i), tc.leaseSID)
					time.Sleep(2 * time.Millisecond)
				}
			}
			if tc.epicFirst {
				inFlightSeat(t, db, epic, "in_progress", "/w/epic", tc.leaseSID)
				time.Sleep(2 * time.Millisecond)
				seatKids()
			} else {
				seatKids()
				inFlightSeat(t, db, epic, "in_progress", "/w/epic", tc.leaseSID)
			}
			setDispatch(t, "coder", "in_progress", epic.ID)

			// The case is only what it claims if the store lists the epic where the
			// case says it is.
			ls, err := db.Leases.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			pos := -1
			for i, l := range ls {
				if l.ItemID == epic.ID {
					pos = i
				}
			}
			wantPos := len(ls) - 1
			if tc.epicFirst {
				wantPos = 0
			}
			if pos != wantPos {
				t.Fatalf("epic listed at %d of %d, want %d (epicFirst=%v)", pos, len(ls), wantPos, tc.epicFirst)
			}

			var before lease.Lease
			if len(kids) > 0 {
				if before, err = db.Leases.Get(context.Background(), kids[0].ID); err != nil {
					t.Fatal(err)
				}
			}
			time.Sleep(10 * time.Millisecond)

			info, engaged, live, err := resolveSeats(true, tc.perfSID)
			if err != nil {
				t.Fatal(err)
			}
			if !engaged || info.ItemID != epic.ID {
				t.Fatalf("performer resolved seat %q (engaged=%v), want its own %s; live=%+v", info.ItemID, engaged, epic.ID, live)
			}
			if !info.InFlight || info.StoryStatus != "ready" {
				t.Errorf("seat must be the in-flight lease over the source status: InFlight=%v StoryStatus=%q", info.InFlight, info.StoryStatus)
			}
			if !hookEditPermitted(info, currentDispatchMarker(), currentRelayMarker()) {
				t.Errorf("a dispatched coder must edit under its own in-flight lease: %+v", info)
			}

			// Attribution never borrows a sibling's lease: its heartbeat is untouched.
			if len(kids) > 0 {
				after, err := db.Leases.Get(context.Background(), kids[0].ID)
				if err != nil {
					t.Fatal(err)
				}
				if !after.HeartbeatAt.Equal(before.HeartbeatAt) {
					t.Errorf("sibling heartbeat moved %v -> %v", before.HeartbeatAt, after.HeartbeatAt)
				}
			}
		})
	}
}

// TestDispatchedPerformerRefusedOutsideItsDispatch (sty_8d7d1c45 AC3): selecting
// the performer's seat grants nothing beyond the route's allocation for it.
func TestDispatchedPerformerRefusedOutsideItsDispatch(t *testing.T) {
	db, create := attributionRepo(t)
	stubWorktree(t, "/w/driver")
	epic := create("epic", "ready")
	sibling := create("sibling", "in_progress")
	settledEpicSeat(t, db, sibling, "in_progress", "/w/sib", "sess-driver")
	inFlightSeat(t, db, epic, "in_progress", "/w/epic", "sess-driver")
	now := time.Now().UTC()

	check := func(t *testing.T, agent, step, item string) string {
		t.Helper()
		setDispatch(t, agent, step, item)
		info, _, live, err := resolveSeats(false, "sess-driver")
		if err != nil {
			t.Fatal(err)
		}
		dm := currentDispatchMarker()
		if hookEditPermitted(info, dm, relayMarker{}) {
			t.Fatalf("%s/%s/%s must be refused (seat %q)", agent, step, item, info.ItemID)
		}
		return hookDenyReason(info, live, dm, relayMarker{}, "sess-driver", now)
	}

	t.Run("wrong agent for the step", func(t *testing.T) {
		check(t, "reviewer", "in_progress", epic.ID)
	})
	t.Run("the sibling's dispatch cannot ride the epic's lease", func(t *testing.T) {
		// The marker names the sibling, whose lease is settled: only the route's
		// committed-step allocation could permit it, and the step is not its status.
		check(t, "coder", "ready", sibling.ID)
	})
	t.Run("a dispatch whose item holds no live lease", func(t *testing.T) {
		got := check(t, "coder", "in_progress", "sty_ghost")
		for _, want := range []string{"sty_ghost", "in_progress", "no live in-flight lease"} {
			if !strings.Contains(got, want) {
				t.Errorf("deny must name the dispatch (%q): %s", want, got)
			}
		}
		if strings.Contains(got, epic.ID) || strings.Contains(got, sibling.ID) {
			t.Errorf("deny must not attribute the dispatch to another story: %s", got)
		}
	})
}

// TestDispatchedPerformerStillFencedOutsideTheRepo (sty_8d7d1c45 AC3): the
// foreign-tree fence runs before the seat is consulted, so a performer holding a
// valid in-flight seat is still denied a write into another git working tree.
func TestDispatchedPerformerStillFencedOutsideTheRepo(t *testing.T) {
	db, create := attributionRepo(t)
	epic := create("epic", "ready")
	inFlightSeat(t, db, epic, "in_progress", "", "")
	setDispatch(t, "coder", "in_progress", epic.ID)

	foreign := t.TempDir()
	if o, err := exec.Command("git", "-C", foreign, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, o)
	}
	out, err := runRootIn(t, editEvent(foreign+"/x.go"), "hook", "gate")
	if err == nil {
		t.Fatalf("a write into another working tree must be denied:\n%s", out)
	}
	if !strings.Contains(out, "allow_outside_tree_edits") {
		t.Errorf("the refusal should name the escape hatch: %s", out)
	}
}
