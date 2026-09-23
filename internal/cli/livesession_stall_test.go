package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
)

// heartbeatOnlySess is a live session whose first turn emits ONE real event
// (a tool start), then goes silent except for heartbeats until cancelled — the
// live-session analogue of agentstep's heartbeatOnlyRunner (sty_752c4ef2 AC4):
// a fake peer that is stuck, not merely slow.
type heartbeatOnlySess struct {
	events  chan agentcli.Event
	onEvent agentcli.EventHandler
	done    chan struct{}
}

func newHeartbeatOnlySess() *heartbeatOnlySess {
	return &heartbeatOnlySess{events: make(chan agentcli.Event, 32), done: make(chan struct{})}
}

func (s *heartbeatOnlySess) emit(ev agentcli.Event) {
	if s.onEvent != nil {
		s.onEvent(ev)
	}
	select {
	case s.events <- ev:
	default:
	}
}

func (s *heartbeatOnlySess) Send(context.Context, agentcli.Turn) error {
	s.emit(agentcli.Event{Kind: agentcli.EventToolStart, Tool: "Bash"})
	go func() {
		t := time.NewTicker(10 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-s.done:
				return
			case <-t.C:
				s.emit(agentcli.Event{Kind: agentcli.EventHeartbeat})
			}
		}
	}()
	return nil
}

func (s *heartbeatOnlySess) Events() <-chan agentcli.Event { return s.events }
func (s *heartbeatOnlySess) Cancel() error                 { close(s.done); return nil }
func (s *heartbeatOnlySess) Close() error                  { return nil }
func (s *heartbeatOnlySess) Captured() []byte              { return nil }

// TestDrainReplyStallsOnHeartbeatOnly (sty_752c4ef2 AC4): drainReply is the
// ONE shared turn primitive for both the interactive chat loop and the
// headless rework relay, so proving it here covers both call sites. A turn
// that emits no real event for idle must end with a *agentstep.StallError,
// not hang until the caller's own context is cancelled.
func TestDrainReplyStallsOnHeartbeatOnly(t *testing.T) {
	sess := newHeartbeatOnlySess()
	if err := sess.Send(context.Background(), agentcli.Turn{Text: "go"}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Cancel() }()

	_, err := drainReply(context.Background(), sess, io.Discard, "coder", 50*time.Millisecond)
	if err == nil {
		t.Fatal("a heartbeat-only turn must stall, not hang forever")
	}
	var se *agentstep.StallError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *agentstep.StallError", err)
	}
	if se.LastEvent != "tool: Bash" {
		t.Errorf("StallError.LastEvent = %q, want %q", se.LastEvent, "tool: Bash")
	}
}

// TestDrainReplyIdleDisabledRunsUntilCompleted proves idle<=0 keeps today's
// behaviour: no stall detection, a turn that never stalls-detects still
// completes normally once the session emits EventCompleted.
func TestDrainReplyIdleDisabledRunsUntilCompleted(t *testing.T) {
	sess := newFakeSess()
	if err := sess.Send(context.Background(), agentcli.Turn{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	reply, err := drainReply(context.Background(), sess, io.Discard, "coder", 0)
	if err != nil {
		t.Fatalf("drainReply: %v", err)
	}
	if reply == "" {
		t.Error("expected a non-empty reply")
	}
}

// TestReworkLoopCoderTurnStalls (sty_752c4ef2 AC4): the rework relay's coder
// side stalls exactly like a one-shot dispatch — the relay ends with an error
// naming the stall rather than hanging on a stuck coder session.
func TestReworkLoopCoderTurnStalls(t *testing.T) {
	consultant := newScriptSess("Something is wrong.\nNOT READY: fix it")
	coder := newHeartbeatOnlySess()
	var out bytes.Buffer
	loop := &reworkLoop{
		Coder: coder, Consultant: consultant,
		CoderRole: "coder", ConsultRole: "consult",
		Rounds: 3, Out: &out, Seed: "review the slice",
		CoderIdleTimeout: 50 * time.Millisecond,
	}
	_, err := loop.Run(context.Background())
	if err == nil {
		t.Fatal("a stalled coder turn must end the relay with an error, not hang")
	}
	var se *agentstep.StallError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *agentstep.StallError", err)
	}
}
