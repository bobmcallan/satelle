package lease

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// seatOpts is the shared-key, tree-anchored claim shape these cases use.
func seatOpts(itemID, owner, key, tree string) AcquireOpts {
	return AcquireOpts{
		ItemID: itemID, Kind: "story", Owner: owner, State: "plan",
		StorySeat: true, SeatKey: key, Worktree: tree,
	}
}

// TestSeatKeyArbitrationMatrix (sty_c098dc2d AC2): the four arbitration cases,
// expressed purely as keys — the lease package never learns which mode chose
// them. A shared key admits co-holders from distinct trees; a different key is
// refused and the refusal carries the holder so the caller can name the key.
func TestSeatKeyArbitrationMatrix(t *testing.T) {
	ctx := context.Background()

	t.Run("admit sibling under one key", func(t *testing.T) {
		s := openTestDB(t)
		if _, out, _, err := s.AcquireWith(ctx, seatOpts("sty_a", "alice", "epic_1", "/w/a")); err != nil || out != OutcomeAcquired {
			t.Fatalf("first sibling: out=%v err=%v", out, err)
		}
		l, out, holder, err := s.AcquireWith(ctx, seatOpts("sty_b", "alice", "epic_1", "/w/b"))
		if err != nil || out != OutcomeAcquired {
			t.Fatalf("sibling under same key must be admitted: out=%v holder=%v err=%v", out, holder, err)
		}
		if l.SeatKey != "epic_1" || l.Worktree != "/w/b" {
			t.Fatalf("sibling lease lost its key/tree: %+v", l)
		}
		all, err := s.List(ctx)
		if err != nil || len(all) != 2 {
			t.Fatalf("both sub-leases must persist: %d %v", len(all), err)
		}
	})

	t.Run("refuse stranger under a different key", func(t *testing.T) {
		s := openTestDB(t)
		if _, _, _, err := s.AcquireWith(ctx, seatOpts("sty_a", "alice", "epic_1", "/w/a")); err != nil {
			t.Fatal(err)
		}
		_, out, holder, err := s.AcquireWith(ctx, seatOpts("sty_x", "bob", "epic_2", "/w/x"))
		if err != nil || out != OutcomeConflict {
			t.Fatalf("different key must conflict: out=%v err=%v", out, err)
		}
		if holder == nil || holder.SeatKey != "epic_1" {
			t.Fatalf("conflict must name the holding key: %+v", holder)
		}
	})

	t.Run("refuse singleton while a shared key holds", func(t *testing.T) {
		s := openTestDB(t)
		if _, _, _, err := s.AcquireWith(ctx, seatOpts("sty_a", "alice", "epic_1", "/w/a")); err != nil {
			t.Fatal(err)
		}
		// A parentless story keys on its OWN id — no key can match it.
		_, out, holder, err := s.AcquireWith(ctx, seatOpts("sty_lone", "bob", "sty_lone", "/w/l"))
		if err != nil || out != OutcomeConflict {
			t.Fatalf("parentless claim must conflict: out=%v err=%v", out, err)
		}
		if holder == nil || holder.ItemID != "sty_a" {
			t.Fatalf("conflict holder = %+v", holder)
		}
	})

	t.Run("singleton claims first, siblings then refused", func(t *testing.T) {
		s := openTestDB(t)
		if _, out, _, err := s.AcquireWith(ctx, seatOpts("sty_lone", "alice", "sty_lone", "/w/l")); err != nil || out != OutcomeAcquired {
			t.Fatalf("singleton claim: out=%v err=%v", out, err)
		}
		// The singleton's key is its own id, so even a would-be sibling group
		// cannot join: single occupancy for the duration.
		_, out, holder, err := s.AcquireWith(ctx, seatOpts("sty_b", "bob", "epic_1", "/w/b"))
		if err != nil || out != OutcomeConflict {
			t.Fatalf("nothing may join a singleton: out=%v err=%v", out, err)
		}
		if holder == nil || holder.ItemID != "sty_lone" {
			t.Fatalf("conflict holder = %+v", holder)
		}
	})
}

// TestSeatDefaultModeUnchangedByKeying (sty_c098dc2d AC1): the legacy Acquire
// signature keys on the item's own id, which IS single occupancy — a second
// story conflicts, and it does so as a seat conflict (never a tree conflict),
// so the default mode's refusal path is untouched.
func TestSeatDefaultModeUnchangedByKeying(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	if _, out, _, err := s.Acquire(ctx, "sty_a", "story", "alice", "plan", true); err != nil || out != OutcomeAcquired {
		t.Fatalf("first: out=%v err=%v", out, err)
	}
	_, out, holder, err := s.Acquire(ctx, "sty_b", "story", "bob", "plan", true)
	if err != nil || out != OutcomeConflict {
		t.Fatalf("second story must be a SEAT conflict: out=%v err=%v", out, err)
	}
	if holder.SeatKey != "sty_a" {
		t.Fatalf("unkeyed acquire must key on its own id: %+v", holder)
	}
	// Even from the same tree: with unique keys the key rule answers first.
	_, out, _, err = s.AcquireWith(ctx, seatOpts("sty_c", "bob", "sty_c", "/w/a"))
	if err != nil || out != OutcomeConflict {
		t.Fatalf("unique-key claim must be a seat conflict, not tree: out=%v err=%v", out, err)
	}
}

// TestSeatTreeConflict (sty_c098dc2d AC3): a valid sibling engaged from an
// ALREADY-LEASED tree is refused with the tree sentinel, not admitted and not
// mislabelled as a seat conflict. An unanchored (empty) tree never participates.
func TestSeatTreeConflict(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	if _, _, _, err := s.AcquireWith(ctx, seatOpts("sty_a", "alice", "epic_1", "/w/shared")); err != nil {
		t.Fatal(err)
	}
	_, out, holder, err := s.AcquireWith(ctx, seatOpts("sty_b", "alice", "epic_1", "/w/shared"))
	if err != nil || out != OutcomeTreeConflict {
		t.Fatalf("same-tree sibling must be a tree conflict: out=%v err=%v", out, err)
	}
	if holder == nil || holder.ItemID != "sty_a" || holder.Worktree != "/w/shared" {
		t.Fatalf("tree conflict must name the tree's holder: %+v", holder)
	}
	// A distinct tree under the same key is fine.
	if _, out, _, err := s.AcquireWith(ctx, seatOpts("sty_b", "alice", "epic_1", "/w/other")); err != nil || out != OutcomeAcquired {
		t.Fatalf("distinct tree must be admitted: out=%v err=%v", out, err)
	}
	// Unanchored claims opt out of tree arbitration entirely.
	if _, out, _, err := s.AcquireWith(ctx, seatOpts("sty_c", "alice", "epic_1", "")); err != nil || out != OutcomeAcquired {
		t.Fatalf("unanchored claim must not be tree-blocked: out=%v err=%v", out, err)
	}
}

// TestSubLeaseLifecycle (sty_c098dc2d AC4): release, stale reap and stop-request
// are per sub-lease. The shared seat is not an object with its own lifetime — it
// exists exactly as long as some sub-lease does.
func TestSubLeaseLifecycle(t *testing.T) {
	ctx := context.Background()

	t.Run("partial release leaves the seat held", func(t *testing.T) {
		s := openTestDB(t)
		mustAcquire(t, s, seatOpts("sty_a", "alice", "epic_1", "/w/a"))
		mustAcquire(t, s, seatOpts("sty_b", "alice", "epic_1", "/w/b"))
		if err := s.Release(ctx, "sty_a", "alice"); err != nil {
			t.Fatal(err)
		}
		all, _ := s.List(ctx)
		if len(all) != 1 || all[0].ItemID != "sty_b" {
			t.Fatalf("releasing one sibling must leave the other: %+v", all)
		}
		// The key is still claimed: a stranger stays refused.
		if _, out, _, _ := s.AcquireWith(ctx, seatOpts("sty_x", "bob", "epic_2", "/w/x")); out != OutcomeConflict {
			t.Fatalf("last sub-lease must still hold the seat: out=%v", out)
		}
		// Last release frees it.
		if err := s.Release(ctx, "sty_b", "alice"); err != nil {
			t.Fatal(err)
		}
		if _, out, _, _ := s.AcquireWith(ctx, seatOpts("sty_x", "bob", "epic_2", "/w/x")); out != OutcomeAcquired {
			t.Fatalf("seat must free with the last sub-lease: out=%v", out)
		}
	})

	t.Run("stale reap takes one sibling only", func(t *testing.T) {
		s := openTestDB(t)
		mustAcquire(t, s, seatOpts("sty_a", "alice", "epic_1", "/w/a"))
		mustAcquire(t, s, seatOpts("sty_b", "alice", "epic_1", "/w/b"))
		if err := s.SetHeartbeat(ctx, "sty_a", time.Now().UTC().Add(-HeartbeatTTL-time.Minute)); err != nil {
			t.Fatal(err)
		}
		reaped, err := s.Reap(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(reaped) != 1 || reaped[0].ItemID != "sty_a" {
			t.Fatalf("reap must take only the stale sibling: %+v", reaped)
		}
		l, err := s.Get(ctx, "sty_b")
		if err != nil || l.SeatKey != "epic_1" {
			t.Fatalf("live sibling must survive intact: %+v %v", l, err)
		}
	})

	t.Run("stop-request marks one sibling only", func(t *testing.T) {
		s := openTestDB(t)
		mustAcquire(t, s, seatOpts("sty_a", "alice", "epic_1", "/w/a"))
		mustAcquire(t, s, seatOpts("sty_b", "alice", "epic_1", "/w/b"))
		if err := s.RequestStop(ctx, "sty_a", "operator", "hand over"); err != nil {
			t.Fatal(err)
		}
		a, _ := s.Get(ctx, "sty_a")
		b, _ := s.Get(ctx, "sty_b")
		if a.StopRequestedBy != "operator" {
			t.Fatalf("stop request missing on target: %+v", a)
		}
		if b.StopRequestedBy != "" {
			t.Fatalf("stop request must not spill onto the sibling: %+v", b)
		}
	})
}

// TestHeartbeatRoutesByWorktree (sty_c098dc2d AC4, the fix verification forces):
// two sibling leases held by the SAME pid-less local@host owner are
// indistinguishable by owner. Selecting by the recorded worktree is what keeps
// the actively-worked sibling alive — route by owner alone and the other
// sibling's heartbeat is the one refreshed, letting this one die at TTL.
func TestHeartbeatRoutesByWorktree(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	const owner = "local@host" // one host, one owner string, two leases
	mustAcquire(t, s, seatOpts("sty_a", owner, "epic_1", "/w/a"))
	mustAcquire(t, s, seatOpts("sty_b", owner, "epic_1", "/w/b"))

	// Both start stale-aged; only the session working in /w/a is active.
	old := time.Now().UTC().Add(-HeartbeatTTL - time.Minute)
	for _, id := range []string{"sty_a", "sty_b"} {
		if err := s.SetHeartbeat(ctx, id, old); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mine, ok := PickForWorktree(all, "/w/a")
	if !ok || mine.ItemID != "sty_a" {
		t.Fatalf("PickForWorktree(/w/a) = %+v ok=%v", mine, ok)
	}
	if err := s.Heartbeat(ctx, mine.ItemID, owner); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	a, _ := s.Get(ctx, "sty_a")
	b, _ := s.Get(ctx, "sty_b")
	if IsStale(a, now) {
		t.Errorf("worked sibling must be refreshed: %+v", a)
	}
	if !IsStale(b, now) {
		t.Errorf("idle sibling must NOT be refreshed by its sibling's activity: %+v", b)
	}
	// No tree, or an unknown tree, must not silently pick someone else's lease.
	if _, ok := PickForWorktree(all, ""); ok {
		t.Error("empty worktree must not match a lease")
	}
	if _, ok := PickForWorktree(all, "/w/elsewhere"); ok {
		t.Error("unknown worktree must not match a lease")
	}
}

// TestMigrateSeatArbitrationColumns (sty_c098dc2d): a DB created before the
// columns existed migrates in place, and a lease held ACROSS that upgrade keeps
// behaving as it did — empty key is a singleton, empty tree is unanchored.
func TestMigrateSeatArbitrationColumns(t *testing.T) {
	db, err := sql.Open("sqlite", "file:lease-seatkey-"+t.Name()+"?mode=memory&cache=shared&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE engagement_lease (
		item_id TEXT PRIMARY KEY, kind TEXT, story_seat INTEGER, owner TEXT NOT NULL,
		state TEXT, acquired_at TEXT NOT NULL, heartbeat_at TEXT NOT NULL,
		stop_requested_by TEXT NOT NULL DEFAULT '', stop_reason TEXT NOT NULL DEFAULT '',
		in_flight INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	nowS := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO engagement_lease (item_id, kind, story_seat, owner, state, acquired_at, heartbeat_at, in_flight)
		VALUES ('sty_old', 'story', 1, 'alice', 'plan', ?, ?, 0)`, nowS, nowS); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate over pre-column schema: %v", err)
	}
	s := New(db)
	ctx := context.Background()
	old, err := s.Get(ctx, "sty_old")
	if err != nil {
		t.Fatal(err)
	}
	if old.SeatKey != "" || old.Worktree != "" {
		t.Fatalf("pre-upgrade row must migrate with empty key/tree: %+v", old)
	}
	// Empty key = singleton: nothing joins it, whatever key it offers.
	if _, out, _, _ := s.AcquireWith(ctx, seatOpts("sty_new", "bob", "epic_1", "/w/n")); out != OutcomeConflict {
		t.Fatalf("empty-key holder must behave as a singleton: out=%v", out)
	}
	// The holder's own next step backfills its key and tree.
	if _, out, _, err := s.AcquireWith(ctx, seatOpts("sty_old", "alice", "epic_1", "/w/o")); err != nil || out != OutcomeAlreadyHeld {
		t.Fatalf("holder's own step: out=%v err=%v", out, err)
	}
	old, _ = s.Get(ctx, "sty_old")
	if old.SeatKey != "epic_1" || old.Worktree != "/w/o" {
		t.Fatalf("next step must backfill key/tree: %+v", old)
	}
}

// TestSeatAnchorNotRewrittenBySequentialStep (sty_c098dc2d): the tree anchor is
// recorded ONCE. A later step run from somewhere else must not silently
// re-anchor the lease — that would move the target `story diff` refuses against.
func TestSeatAnchorNotRewrittenBySequentialStep(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	mustAcquire(t, s, seatOpts("sty_a", "alice", "epic_1", "/w/a"))
	if err := s.Confirm(ctx, "sty_a", "plan"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.AcquireWith(ctx, seatOpts("sty_a", "alice", "epic_1", "/w/elsewhere")); err != nil {
		t.Fatal(err)
	}
	l, _ := s.Get(ctx, "sty_a")
	if l.Worktree != "/w/a" {
		t.Fatalf("anchor must survive a step run elsewhere: %+v", l)
	}
}

// TestTaskLeaseNeverHoldsSeat (sty_c098dc2d AC5): task leases coexist with the
// story seat and never occupy it — neither blocking a story nor being blocked.
func TestTaskLeaseNeverHoldsSeat(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	if _, out, _, err := s.AcquireWith(ctx, AcquireOpts{
		ItemID: "tsk_1", Kind: "task", Owner: "alice", State: "in_progress",
		StorySeat: false, SeatKey: "tsk_1", Worktree: "/w/a",
	}); err != nil || out != OutcomeAcquired {
		t.Fatalf("task lease: out=%v err=%v", out, err)
	}
	// A story engages from the SAME tree — the task lease is not a seat holder,
	// so neither the key nor the tree rule sees it.
	if _, out, _, err := s.AcquireWith(ctx, seatOpts("sty_a", "alice", "sty_a", "/w/a")); err != nil || out != OutcomeAcquired {
		t.Fatalf("task lease must not block the story seat: out=%v err=%v", out, err)
	}
}

func mustAcquire(t *testing.T, s *Store, opts AcquireOpts) {
	t.Helper()
	if _, out, holder, err := s.AcquireWith(context.Background(), opts); err != nil || out != OutcomeAcquired {
		t.Fatalf("acquire %s: out=%v holder=%+v err=%v", opts.ItemID, out, holder, err)
	}
}
