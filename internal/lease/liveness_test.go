package lease

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/testutil"
)

// holderGone makes every pid probe answer "dead" for the test: the process that
// stamped the seat's in-flight mark exited without settling it (the kill shape).
func holderGone(t *testing.T) {
	t.Helper()
	prev := PidAlive
	PidAlive = func(int) bool { return false }
	t.Cleanup(func() { PidAlive = prev })
}

// pidsAlive is the opposite: the seat's holder is running, whatever the clock says.
func pidsAlive(t *testing.T) {
	t.Helper()
	prev := PidAlive
	PidAlive = func(int) bool { return true }
	t.Cleanup(func() { PidAlive = prev })
}

func ageHeartbeat(t *testing.T, s *Store, item string) {
	t.Helper()
	// time-subject: the heartbeat TTL is what these tests judge. It is crossed by
	// backdating the heartbeat, never by waiting for it.
	old := time.Now().UTC().Add(-HeartbeatTTL - time.Minute)
	if err := s.SetHeartbeat(context.Background(), item, old); err != nil {
		t.Fatal(err)
	}
}

// TestAliveLongGate: a gate runs past HeartbeatTTL with no heartbeat, but the
// process that started it is alive — the seat is neither dead to Alive, nor
// reaped, nor stolen (sty_7f3e6fd3 AC1).
func TestAliveLongGate(t *testing.T) {
	pidsAlive(t)
	s := openTestDB(t)
	ctx := context.Background()
	mustAcquire(t, s, AcquireOpts{ItemID: "sty_a", Kind: "story", Owner: "alice", State: "plan", StorySeat: true, SeatKey: "sty_a"})
	ageHeartbeat(t, s, "sty_a")
	l, _ := s.Get(ctx, "sty_a")
	if !l.InFlight || l.InFlightPid == 0 {
		t.Fatalf("precondition: seat must be mid-transition with a pid: %+v", l)
	}
	if !Alive(l, time.Now().UTC()) {
		t.Fatal("a live in-flight dispatch must keep the seat Alive past HeartbeatTTL")
	}
	reaped, err := s.Reap(ctx)
	if err != nil || len(reaped) != 0 {
		t.Fatalf("Reap must not take a live seat: reaped=%+v err=%v", reaped, err)
	}
	if _, err := s.Get(ctx, "sty_a"); err != nil {
		t.Fatalf("seat dropped during a long gate: %v", err)
	}
}

// TestAliveRelayRun: the settled seat's owner embeds the pid of the relay's
// holder; the heartbeat is expired but that process is alive.
func TestAliveRelayRun(t *testing.T) {
	pidsAlive(t)
	host, _ := os.Hostname()
	if host == "" {
		t.Skip("no hostname")
	}
	owner := "local@" + host + ":" + strconv.Itoa(os.Getpid())
	s := openTestDB(t)
	ctx := context.Background()
	mustAcquire(t, s, AcquireOpts{ItemID: "sty_a", Kind: "story", Owner: owner, State: "in_progress", StorySeat: true, SeatKey: "sty_a"})
	if err := s.Confirm(ctx, "sty_a", "in_progress"); err != nil {
		t.Fatal(err)
	}
	ageHeartbeat(t, s, "sty_a")
	l, _ := s.Get(ctx, "sty_a")
	if !Alive(l, time.Now().UTC()) {
		t.Fatal("a live owner pid must keep the seat Alive past HeartbeatTTL")
	}
	// The same seat with its holder dead is not Alive, however fresh the heartbeat.
	holderGone(t)
	if err := s.Heartbeat(ctx, "sty_a", owner); err != nil {
		t.Fatal(err)
	}
	l, _ = s.Get(ctx, "sty_a")
	if Alive(l, time.Now().UTC()) {
		t.Fatal("a dead owner pid must end the seat even with a fresh heartbeat")
	}
}

// TestAcquireRefusesStealOfLiveSeat: another owner cannot take a seat whose
// dispatch is alive, even with the heartbeat long expired.
func TestAcquireRefusesStealOfLiveSeat(t *testing.T) {
	pidsAlive(t)
	s := openTestDB(t)
	ctx := context.Background()
	mustAcquire(t, s, AcquireOpts{ItemID: "sty_a", Kind: "story", Owner: "alice", State: "plan", StorySeat: true, SeatKey: "sty_a"})
	ageHeartbeat(t, s, "sty_a")
	_, out, holder, err := s.Acquire(ctx, "sty_a", "story", "bob", "plan", true)
	if err != nil || out != OutcomeConflict || holder == nil || holder.Owner != "alice" {
		t.Fatalf("steal of a live seat: out=%v holder=%+v err=%v", out, holder, err)
	}
	// A different item cannot take the story seat from it either.
	_, out, _, err = s.Acquire(ctx, "sty_b", "story", "bob", "plan", true)
	if err != nil || out != OutcomeConflict {
		t.Fatalf("seat steal by another story: out=%v err=%v", out, err)
	}
}

// TestAliveDeadHolderExpired: the regression the predicate must keep — holder
// gone and heartbeat expired means not Alive, reaped and stealable.
func TestAliveDeadHolderExpired(t *testing.T) {
	holderGone(t)
	s := openTestDB(t)
	ctx := context.Background()
	mustAcquire(t, s, AcquireOpts{ItemID: "sty_a", Kind: "story", Owner: "alice", State: "plan", StorySeat: true, SeatKey: "sty_a"})
	ageHeartbeat(t, s, "sty_a")
	l, _ := s.Get(ctx, "sty_a")
	if Alive(l, time.Now().UTC()) {
		t.Fatal("dead holder + expired heartbeat must not be Alive")
	}
	reaped, err := s.Reap(ctx)
	if err != nil || len(reaped) != 1 {
		t.Fatalf("Reap: reaped=%+v err=%v", reaped, err)
	}
}

// TestKeepAliveKeepsLongDispatchInFlight (sty_7f3e6fd3 AC3): a dispatch that
// outlasts InFlightTTL — a coder working for hours — must still read as in
// flight, because the edit gate authorises the dispatched coder only while it
// does. The beat refreshes in_flight_at for a mid-transition row; a settled row
// keeps its own in_flight_at, and a plain Heartbeat (the hooks') never touches it.
func TestKeepAliveKeepsLongDispatchInFlight(t *testing.T) {
	prev := KeepAliveInterval
	KeepAliveInterval = 5 * time.Millisecond
	t.Cleanup(func() { KeepAliveInterval = prev })

	s := openTestDB(t)
	ctx := context.Background()
	mustAcquire(t, s, AcquireOpts{ItemID: "sty_a", Kind: "story", Owner: "alice", State: "plan", StorySeat: true, SeatKey: "sty_a"})
	if err := s.SetInFlightAt(ctx, "sty_a", time.Now().UTC().Add(-InFlightTTL-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat(ctx, "sty_a", "alice"); err != nil {
		t.Fatal(err)
	}
	l, _ := s.Get(ctx, "sty_a")
	if EffectiveInFlight(l, time.Now().UTC()) {
		t.Fatal("precondition: a plain heartbeat must not refresh in_flight_at")
	}

	stop := KeepAlive(ctx, s, "sty_a", "alice")
	testutil.Eventually(t, testutil.WaitBudget, func() bool {
		l, _ = s.Get(ctx, "sty_a")
		return EffectiveInFlight(l, time.Now().UTC())
	}, "KeepAlive never refreshed in_flight_at for a live dispatch")
	stop()

	// Settled: the beat must not resurrect an in-flight mark.
	if err := s.Confirm(ctx, "sty_a", "plan"); err != nil {
		t.Fatal(err)
	}
	if err := s.beat(ctx, "sty_a", "alice"); err != nil {
		t.Fatal(err)
	}
	l, _ = s.Get(ctx, "sty_a")
	if l.InFlight || EffectiveInFlight(l, time.Now().UTC()) {
		t.Fatalf("a beat on a settled row must not mark it in flight: %+v", l)
	}
}

// TestKeepAliveHoldsSeatAcrossTTL: with no pid probe to vouch for the holder, a
// running KeepAlive keeps the heartbeat inside the TTL for as long as it runs
// and stops cleanly.
func TestKeepAliveHoldsSeatAcrossTTL(t *testing.T) {
	holderGone(t)
	prev := KeepAliveInterval
	KeepAliveInterval = 5 * time.Millisecond
	t.Cleanup(func() { KeepAliveInterval = prev })

	s := openTestDB(t)
	ctx := context.Background()
	mustAcquire(t, s, AcquireOpts{ItemID: "sty_a", Kind: "story", Owner: "alice", State: "plan", StorySeat: true, SeatKey: "sty_a"})
	ageHeartbeat(t, s, "sty_a")
	l, _ := s.Get(ctx, "sty_a")
	if Alive(l, time.Now().UTC()) {
		t.Fatal("precondition: seat must be stale before KeepAlive")
	}
	stop := KeepAlive(ctx, s, "sty_a", "alice")
	defer stop()
	testutil.Eventually(t, testutil.WaitBudget, func() bool {
		l, _ = s.Get(ctx, "sty_a")
		return Alive(l, time.Now().UTC())
	}, "KeepAlive never refreshed the heartbeat")
	stop()
	stop() // idempotent: every return path may call it
}
