package lease

import (
	"context"
	"os"
	"time"
)

// Alive is the ONE liveness predicate for a seat (sty_7f3e6fd3). Acquire's
// steal path, Reap and every seat consumer (edit gate, Stop hook, seat views)
// call it; a seat is stale exactly when it is not Alive.
//
// A seat is alive while whoever holds it is still running:
//  1. an owner that embeds a same-host pid ("local@host:pid") is alive exactly
//     while that process is — the process is authoritative either way, so a
//     long heartbeat gap does not drop a live holder;
//  2. otherwise, a transition it started (in_flight with a live in_flight_pid)
//     keeps the seat alive however long the gate or relay runs;
//  3. otherwise the heartbeat decides: alive while it is inside HeartbeatTTL
//     (a zero heartbeat is not aged).
//
// The pid probe is host-local (PidAlive); a holder on another host falls to the
// heartbeat, which KeepAlive keeps fresh across a long run.
func Alive(l Lease, now time.Time) bool {
	if host, pid, ok := parseLocalOwner(l.Owner); ok {
		if myHost, _ := os.Hostname(); myHost != "" && host == myHost {
			return pidAlive(pid)
		}
	}
	if l.InFlight && l.InFlightPid > 0 && pidAlive(l.InFlightPid) {
		return true
	}
	return l.HeartbeatAt.IsZero() || now.Sub(l.HeartbeatAt) <= HeartbeatTTL
}

// KeepAliveInterval is how often KeepAlive refreshes the heartbeat: a quarter
// of HeartbeatTTL, so several beats fit inside one TTL.
var KeepAliveInterval = HeartbeatTTL / 4

// KeepAlive refreshes the owner's heartbeat every KeepAliveInterval until the
// returned stop is called (stop blocks until the goroutine has exited, so it is
// safe to defer on every return path). It holds a seat across a long gate or
// relay run — work that fires no hook of its own — when no pid probe can vouch
// for the holder. While the row is mid-transition it also refreshes in_flight_at:
// the process running the transition is the one calling this, so a dispatch that
// outlasts InFlightTTL (a coder working for hours) is still in flight, and
// EffectiveInFlight — which the edit gate reads — must say so. A crashed
// process stops beating, so its flag still ages out. Best-effort: an error is
// discarded, exactly as the hook-driven touch is.
func KeepAlive(ctx context.Context, s *Store, itemID, owner string) (stop func()) {
	if s == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	interval := KeepAliveInterval
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = s.beat(ctx, itemID, owner)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// beat is one KeepAlive tick: heartbeat_at always, in_flight_at only while the
// row is mid-transition. Kept apart from Heartbeat on purpose — hooks call that
// on every tool use, and letting them refresh in_flight_at would wedge a stuck
// flag forever (sty_bf797fa9).
func (s *Store) beat(ctx context.Context, itemID, owner string) error {
	nowS := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx,
		`UPDATE engagement_lease
		 SET heartbeat_at = ?, in_flight_at = CASE WHEN in_flight = 1 THEN ? ELSE in_flight_at END
		 WHERE item_id = ? AND owner = ?`, nowS, nowS, itemID, owner)
	return err
}
