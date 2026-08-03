package lease

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", "file:lease-test-"+t.Name()+"?mode=memory&cache=shared&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	return New(db)
}

func TestAcquireConflictDifferentStories(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	_, out, _, err := s.Acquire(ctx, "sty_a", "story", "alice", "plan", true)
	if err != nil || out != OutcomeAcquired {
		t.Fatalf("first acquire: out=%v err=%v", out, err)
	}
	_, out, holder, err := s.Acquire(ctx, "sty_b", "story", "bob", "plan", true)
	if out != OutcomeConflict || holder == nil {
		t.Fatalf("second story must conflict: out=%v holder=%v err=%v", out, holder, err)
	}
	if holder.ItemID != "sty_a" || holder.Owner != "alice" {
		t.Fatalf("holder = %+v, want sty_a/alice", holder)
	}
}

func TestAcquireSameStoryDedup(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	_, out, _, err := s.Acquire(ctx, "sty_a", "story", "alice", "plan", true)
	if err != nil || out != OutcomeAcquired {
		t.Fatalf("first: out=%v err=%v", out, err)
	}
	// Concurrent re-acquire while in_flight → OutcomeInFlight (AC3).
	_, out, _, err = s.Acquire(ctx, "sty_a", "story", "alice", "plan", true)
	if err != nil || out != OutcomeInFlight {
		t.Fatalf("in-flight re-acquire: out=%v err=%v", out, err)
	}
	// Settle, then sequential step.
	if err := s.Confirm(ctx, "sty_a", "plan"); err != nil {
		t.Fatal(err)
	}
	l, out, _, err := s.Acquire(ctx, "sty_a", "story", "alice", "in_progress", true)
	if err != nil || out != OutcomeAlreadyHeld {
		t.Fatalf("sequential step: out=%v err=%v", out, err)
	}
	if l.State != "plan" {
		t.Fatalf("state must stay last-committed until Confirm, got %q", l.State)
	}
	if !l.InFlight {
		t.Fatal("sequential step must mark in_flight")
	}
	if err := s.Confirm(ctx, "sty_a", "in_progress"); err != nil {
		t.Fatal(err)
	}
	ok, err := s.AnyActive(ctx)
	if err != nil || !ok {
		t.Fatalf("any active: %v %v", ok, err)
	}
}

func TestReleaseOwnerOnly(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	_, _, _, _ = s.Acquire(ctx, "sty_a", "story", "alice", "plan", true)
	if err := s.Release(ctx, "sty_a", "bob"); err == nil {
		t.Fatal("non-owner release must fail")
	}
	if err := s.Release(ctx, "sty_a", "alice"); err != nil {
		t.Fatal(err)
	}
	// Seat free — bob can acquire
	_, out, _, err := s.Acquire(ctx, "sty_b", "story", "bob", "plan", true)
	if err != nil || out != OutcomeAcquired {
		t.Fatalf("after release: out=%v err=%v", out, err)
	}
}

func TestStaleLeaseStolen(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	// Force stale via old heartbeat by inserting directly then stealing.
	_, _, _, _ = s.Acquire(ctx, "sty_a", "story", "dead-owner", "plan", true)
	// Manually age heartbeat
	old := time.Now().UTC().Add(-HeartbeatTTL - time.Minute).Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`UPDATE engagement_lease SET heartbeat_at = ? WHERE item_id = ?`, old, "sty_a"); err != nil {
		t.Fatal(err)
	}
	_, out, stolen, err := s.Acquire(ctx, "sty_a", "story", "alice", "plan", true)
	if err != nil || out != OutcomeStolen {
		t.Fatalf("stale steal: out=%v err=%v", out, err)
	}
	if stolen == nil || stolen.Owner != "dead-owner" {
		t.Fatalf("stolen holder = %+v", stolen)
	}
}

func TestRequestStopAnnotatesOnly(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	_, _, _, _ = s.Acquire(ctx, "sty_a", "story", "alice", "plan", true)
	if err := s.RequestStop(ctx, "sty_a", "bob", "please stop"); err != nil {
		t.Fatal(err)
	}
	l, err := s.Get(ctx, "sty_a")
	if err != nil {
		t.Fatal(err)
	}
	if l.Owner != "alice" || l.StopRequestedBy != "bob" || l.StopReason != "please stop" {
		t.Fatalf("lease after stop request: %+v", l)
	}
	// Owner still holds
	if err := s.Release(ctx, "sty_a", "bob"); err == nil {
		t.Fatal("stop request must not transfer ownership")
	}
}

func TestConcurrentDifferentStoriesOneWins(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	acquired, conflicts := 0, 0
	owners := []string{"alice", "bob"}
	ids := []string{"sty_a", "sty_b"}
	for i := range ids {
		wg.Add(1)
		go func(id, owner string) {
			defer wg.Done()
			_, out, _, _ := s.Acquire(ctx, id, "story", owner, "plan", true)
			mu.Lock()
			defer mu.Unlock()
			switch out {
			case OutcomeAcquired, OutcomeStolen:
				acquired++
			case OutcomeConflict:
				conflicts++
			}
		}(ids[i], owners[i])
	}
	wg.Wait()
	if acquired != 1 || conflicts != 1 {
		t.Fatalf("want 1 acquired + 1 conflict, got acquired=%d conflicts=%d", acquired, conflicts)
	}
}

func TestAcquireWithoutStorySeat(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	// occupiesStorySeat=false (tasks / non-story) — both admitted
	_, out1, _, err := s.Acquire(ctx, "sty_a", "story", "alice", "plan", false)
	if err != nil || out1 != OutcomeAcquired {
		t.Fatalf("a: %v %v", out1, err)
	}
	_, out2, _, err := s.Acquire(ctx, "sty_b", "story", "bob", "plan", false)
	if err != nil || out2 != OutcomeAcquired {
		t.Fatalf("b: %v %v", out2, err)
	}
}

// TestEffectiveInFlightAgesStuckFlag (sty_bf797fa9): a fresh heartbeat does not
// keep an expired in_flight live for gate purposes; Acquire treats it as settled.
func TestEffectiveInFlightAgesStuckFlag(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	_, out, _, err := s.Acquire(ctx, "sty_a", "story", "alice", "plan", true)
	if err != nil || out != OutcomeAcquired {
		t.Fatalf("acquire: out=%v err=%v", out, err)
	}
	// Age in_flight_at past TTL while keeping heartbeat fresh (hook-refresh shape).
	old := time.Now().UTC().Add(-InFlightTTL - time.Minute)
	if err := s.SetInFlightAt(ctx, "sty_a", old); err != nil {
		t.Fatal(err)
	}
	// Heartbeat still current.
	if err := s.Heartbeat(ctx, "sty_a", "alice"); err != nil {
		t.Fatal(err)
	}
	l, err := s.Get(ctx, "sty_a")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if IsStale(l, now) {
		t.Fatal("heartbeat is fresh — must not be IsStale")
	}
	if !l.InFlight {
		t.Fatal("raw InFlight column still set")
	}
	if EffectiveInFlight(l, now) {
		t.Fatal("EffectiveInFlight must be false after InFlightTTL despite fresh heartbeat")
	}
	// Re-acquire: stuck flag self-heals → OutcomeAlreadyHeld (settled path), not InFlight.
	_, out, _, err = s.Acquire(ctx, "sty_a", "story", "alice", "in_progress", true)
	if err != nil || out != OutcomeAlreadyHeld {
		t.Fatalf("expired in_flight re-acquire: out=%v err=%v", out, err)
	}
}

func TestEffectiveInFlightLiveWithinTTL(t *testing.T) {
	now := time.Now().UTC()
	l := Lease{InFlight: true, InFlightAt: now.Add(-time.Minute), HeartbeatAt: now}
	if !EffectiveInFlight(l, now) {
		t.Fatal("fresh in_flight_at must be live")
	}
	if EffectiveInFlight(Lease{InFlight: false, InFlightAt: now}, now) {
		t.Fatal("InFlight=false must not be live")
	}
}

// TestEffectiveInFlightZeroAtNotLive: missing InFlightAt must not fall back to
// heartbeat freshness (hooks keep heartbeat current and would wedge forever).
func TestEffectiveInFlightZeroAtNotLive(t *testing.T) {
	now := time.Now().UTC()
	l := Lease{InFlight: true, InFlightAt: time.Time{}, HeartbeatAt: now}
	if EffectiveInFlight(l, now) {
		t.Fatal("zero InFlightAt must not be live even with fresh heartbeat")
	}
}

// TestEffectiveInFlightDeadPidNotLive: primary AC3 path — transitioning process
// is gone, so the flag must not block even with a fresh heartbeat and young
// InFlightAt. Does not require changing the pid-less default Owner.
func TestEffectiveInFlightDeadPidNotLive(t *testing.T) {
	now := time.Now().UTC()
	// Pick a pid that is almost certainly not alive.
	dead := 1<<30 - 1
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", dead)); err == nil {
		t.Skip("improbable live pid; skip")
	}
	l := Lease{
		InFlight: true, InFlightAt: now, InFlightPid: dead, HeartbeatAt: now,
	}
	if EffectiveInFlight(l, now) {
		t.Fatal("dead InFlightPid must make EffectiveInFlight false immediately")
	}
	// Live pid (this test process) with young InFlightAt stays live.
	live := Lease{
		InFlight: true, InFlightAt: now, InFlightPid: os.Getpid(), HeartbeatAt: now,
	}
	if !EffectiveInFlight(live, now) {
		t.Fatal("live InFlightPid with young InFlightAt must be live")
	}
}

// TestAcquireSelfHealsDeadInFlightPid: Acquire re-enters after the transitioning
// process dies rather than returning OutcomeInFlight forever.
func TestAcquireSelfHealsDeadInFlightPid(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	_, out, _, err := s.Acquire(ctx, "sty_a", "story", "alice", "plan", true)
	if err != nil || out != OutcomeAcquired {
		t.Fatalf("acquire: out=%v err=%v", out, err)
	}
	// Plant a dead in_flight_pid while keeping heartbeat fresh and in_flight_at young.
	dead := 1<<30 - 1
	if _, err := s.db.Exec(`UPDATE engagement_lease SET in_flight_pid = ? WHERE item_id = ?`, dead, "sty_a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat(ctx, "sty_a", "alice"); err != nil {
		t.Fatal(err)
	}
	// Re-acquire must self-heal to AlreadyHeld (settled path), not InFlight.
	_, out, _, err = s.Acquire(ctx, "sty_a", "story", "alice", "in_progress", true)
	if err != nil || out != OutcomeAlreadyHeld {
		t.Fatalf("dead-pid re-acquire: out=%v err=%v", out, err)
	}
}

// TestMigrateBackfillsInFlightAt: pre-upgrade rows with in_flight=1 and empty
// in_flight_at get heartbeat_at copied so EffectiveInFlight can age them.
func TestMigrateBackfillsInFlightAt(t *testing.T) {
	db, err := sql.Open("sqlite", "file:lease-backfill-"+t.Name()+"?mode=memory&cache=shared&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Pre-column schema shape: create table without in_flight_at, then Migrate.
	if _, err := db.Exec(`CREATE TABLE engagement_lease (
		item_id TEXT PRIMARY KEY, kind TEXT, story_seat INTEGER, owner TEXT,
		state TEXT, acquired_at TEXT, heartbeat_at TEXT,
		stop_requested_by TEXT DEFAULT '', stop_reason TEXT DEFAULT '',
		in_flight INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatal(err)
	}
	oldHB := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO engagement_lease
		(item_id, kind, story_seat, owner, state, acquired_at, heartbeat_at, in_flight)
		VALUES ('sty_stuck', 'story', 1, 'alice', 'integration', ?, ?, 1)`, oldHB, oldHB); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	l, err := s.Get(context.Background(), "sty_stuck")
	if err != nil {
		t.Fatal(err)
	}
	if l.InFlightAt.IsZero() {
		t.Fatal("Migrate must backfill in_flight_at for in_flight=1 rows")
	}
	// Aged past TTL → not effective.
	if EffectiveInFlight(l, time.Now().UTC()) {
		t.Fatal("backfilled aged in_flight_at must not be EffectiveInFlight")
	}
}

// TestListIsStaleReap: List returns rows; IsStale flags aged heartbeats; Reap
// deletes them so a subsequent seat-occupying acquire succeeds (sty_1738f973 AC3).
func TestListIsStaleReap(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	_, _, _, _ = s.Acquire(ctx, "sty_a", "story", "dead", "plan", true)
	old := time.Now().UTC().Add(-HeartbeatTTL - time.Minute).Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`UPDATE engagement_lease SET heartbeat_at = ? WHERE item_id = ?`, old, "sty_a"); err != nil {
		t.Fatal(err)
	}
	all, err := s.List(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("list: %v len=%d", err, len(all))
	}
	now := time.Now().UTC()
	if !IsStale(all[0], now) {
		t.Fatal("aged lease must be IsStale")
	}
	reaped, err := s.Reap(ctx)
	if err != nil || len(reaped) != 1 || reaped[0].ItemID != "sty_a" {
		t.Fatalf("reap: %v reaped=%+v", err, reaped)
	}
	all, err = s.List(ctx)
	if err != nil || len(all) != 0 {
		t.Fatalf("after reap list: %v len=%d", err, len(all))
	}
	_, out, _, err := s.Acquire(ctx, "sty_b", "story", "alice", "plan", true)
	if err != nil || out != OutcomeAcquired {
		t.Fatalf("acquire after reap: out=%v err=%v", out, err)
	}
}
