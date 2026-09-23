package agentstep

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// StallError reports that a dispatch produced no REAL event (tool start/end,
// message, usage) for Idle — a heartbeat alone does not reset the clock, so
// this is distinct from a process that is merely slow (sty_752c4ef2). It
// carries the last real event's label and timestamp so the refusal text and
// the telemetry ledger both name what the agent was last seen doing.
type StallError struct {
	Idle        time.Duration
	LastEvent   string
	LastEventAt time.Time
}

func (e *StallError) Error() string {
	last := e.LastEvent
	if last == "" {
		last = "none"
	}
	return fmt.Sprintf("stalled: no activity for %s (last event: %s)", e.Idle.Round(time.Second), last)
}

// StallCause returns ctx's cancellation cause as a *StallError, or nil when
// ctx was not cancelled by a Watchdog (a hard timeout, an upstream cancel, or
// no cancellation at all).
func StallCause(ctx context.Context) *StallError {
	se, _ := context.Cause(ctx).(*StallError)
	return se
}

// isRealEvent reports whether kind counts as progress for the stall detector.
// EventHeartbeat is deliberately excluded: it proves only that the transport
// is still emitting synthetic keepalives, not that the agent is doing
// anything (sty_752c4ef2 evidence #2).
func isRealEvent(kind agentcli.EventKind) bool {
	switch kind {
	case agentcli.EventStart, agentcli.EventToolStart, agentcli.EventToolEnd,
		agentcli.EventMessage, agentcli.EventUsage:
		return true
	default:
		return false
	}
}

// eventLabel renders a short, stable label for a real event — "tool: Bash",
// "message", "usage", "start" — used in the StallError, the refusal text, and
// (order:B, not yet wired) the lease activity record's last-event label.
func eventLabel(ev agentcli.Event) string {
	switch ev.Kind {
	case agentcli.EventToolStart, agentcli.EventToolEnd:
		if ev.Tool != "" {
			return "tool: " + ev.Tool
		}
		return "tool"
	case agentcli.EventMessage:
		return "message"
	case agentcli.EventUsage:
		return "usage"
	case agentcli.EventStart:
		return "start"
	default:
		return string(ev.Kind)
	}
}

// minWatchdogTick / maxWatchdogTick bound how often a Watchdog samples the
// idle clock: fast enough that a tiny idle_timeout (a test's 50ms) still
// stalls promptly, slow enough that a real 5m default does not busy-poll.
const (
	minWatchdogTick = 5 * time.Millisecond
	maxWatchdogTick = 5 * time.Second
)

func watchdogTick(idle time.Duration) time.Duration {
	tick := idle / 4
	if tick < minWatchdogTick {
		tick = minWatchdogTick
	}
	if tick > maxWatchdogTick {
		tick = maxWatchdogTick
	}
	return tick
}

// Watchdog cancels a run's context when no real event arrives for idle. One
// Watchdog serves one run — a single agentstep.Invoke dispatch, or one live
// session turn (chatloop/reworkloop's drainReply). It sits ABOVE the
// agentcli.Runner/Session seam: every transport (command, stream, ACP) spawns
// its child through exec.CommandContext(ctx, ...) against the SAME context the
// watchdog wraps, so cancelling it kills the subprocess exactly as the
// existing hard timeout does — no transport-specific code is needed
// (sty_752c4ef2).
type Watchdog struct {
	idle time.Duration
	now  func() time.Time

	mu        sync.Mutex
	lastLabel string
	lastAt    time.Time
	realCount int
}

// NewWatchdog builds a Watchdog bounding idle. idle must be > 0 — callers
// disable stall detection by not constructing one (mirrors the hard-timeout
// timeout<=0 convention).
func NewWatchdog(idle time.Duration) *Watchdog {
	return &Watchdog{idle: idle, now: time.Now}
}

// Start begins watching ctx and returns a derived context that is cancelled
// with a *StallError (retrievable via StallCause / context.Cause) when idle
// elapses with no Touch/TouchEvent call, plus a stop func that MUST be
// deferred to release the watchdog's goroutine.
func (w *Watchdog) Start(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	w.mu.Lock()
	w.lastAt = w.now()
	w.lastLabel = "start"
	w.mu.Unlock()

	done := make(chan struct{})
	go func() {
		t := time.NewTicker(watchdogTick(w.idle))
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case now := <-t.C:
				w.mu.Lock()
				idleFor := now.Sub(w.lastAt)
				label, at := w.lastLabel, w.lastAt
				w.mu.Unlock()
				if idleFor >= w.idle {
					cancel(&StallError{Idle: idleFor, LastEvent: label, LastEventAt: at})
					return
				}
			}
		}
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() { close(done) })
		cancel(nil)
	}
	return ctx, stop
}

// Touch resets the idle clock, recording label as the last real event.
func (w *Watchdog) Touch(label string) {
	w.mu.Lock()
	w.lastAt = w.now()
	w.lastLabel = label
	w.realCount++
	w.mu.Unlock()
}

// TouchEvent resets the idle clock only for a real event (isRealEvent);
// EventHeartbeat and other synthetic/status events pass through untouched.
func (w *Watchdog) TouchEvent(ev agentcli.Event) {
	if !isRealEvent(ev.Kind) {
		return
	}
	w.Touch(eventLabel(ev))
}

// Snapshot reports the watchdog's current idle state — the last real event's
// label and timestamp, and how many real events it has seen. Used by the
// lease activity refresher (order:B) to report agent progress mid-dispatch.
type WatchdogSnapshot struct {
	LastEvent   string
	LastEventAt time.Time
	EventCount  int
}

// Snapshot returns the watchdog's current state.
func (w *Watchdog) Snapshot() WatchdogSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	return WatchdogSnapshot{LastEvent: w.lastLabel, LastEventAt: w.lastAt, EventCount: w.realCount}
}
