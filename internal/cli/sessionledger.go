package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// sessionLedger is the transcript sink for a hand-opened live session (the
// rework relay). Turns go through story-message; tool/permission decisions are
// invocation rows.
type sessionLedger interface {
	// WriteMessage records one turn. cc is an ADDITIONAL address the row is
	// readable under while to stays who the turn is FOR — "*" for a rework
	// relay turn so the transcript reaches whoever judges the edge
	// (sty_8e0b29a0).
	WriteMessage(from, to, cc, body string) error
	WriteInvocation(tool, kind, decision, decidedBy string) error
	ListMessagesSince(since time.Time, to string) ([]verb.AgentMessage, error)
}

type storeSessionLedger struct {
	ctx     context.Context
	storyID string
	ls      *ledger.Store
	// actor is the binding the session was opened with — the agent whose tool
	// boundaries and permission decisions these invocation rows describe.
	actor string
}

func (s *storeSessionLedger) WriteMessage(from, to, cc, body string) error {
	req := map[string]any{"id": s.storyID, "from": from, "to": to, "body": body}
	if strings.TrimSpace(cc) != "" {
		req["cc"] = cc
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = verb.Dispatch(s.ctx, "story-message", raw)
	return err
}

func (s *storeSessionLedger) WriteInvocation(tool, kind, decision, decidedBy string) error {
	if s.ls == nil {
		return fmt.Errorf("session ledger: no ledger store")
	}
	payload, err := json.Marshal(map[string]any{
		"tool": tool, "kind": kind, "decision": decision, "decided_by": decidedBy,
	})
	if err != nil {
		return err
	}
	body := fmt.Sprintf("session %s %s by %s", decision, tool, decidedBy)
	_, err = s.ls.Append(s.ctx, ledger.AppendInput{
		StoryID: s.storyID,
		Kind:    ledger.KindAgentInvocation,
		Actor:   s.actor,
		Body:    body,
		Payload: payload,
	}, time.Now())
	return err
}

func (s *storeSessionLedger) ListMessagesSince(since time.Time, to string) ([]verb.AgentMessage, error) {
	req := map[string]any{"id": s.storyID, "to": to}
	if !since.IsZero() {
		req["since"] = since.UTC().Format(time.RFC3339Nano)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := verb.Dispatch(s.ctx, "story-messages", raw)
	if err != nil {
		return nil, err
	}
	var msgs []verb.AgentMessage
	if err := json.Unmarshal(resp, &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

// drainReply reads one turn's events off sess until the session completes,
// accumulating the assistant text and rendering progress to out. who names the
// session in the failure message (sty_8e0b29a0).
//
// idle bounds this ONE turn with a Watchdog (sty_752c4ef2): idle ≤0 disables
// stall detection (only ctx cancellation ends the turn early). The watchdog
// sits above the transport exactly as it does for a one-shot dispatch
// (agentstep.runOnce): it wraps ctx and resets its clock on every REAL event
// pulled off sess.Events() (heartbeats do not reset it), and a fired watchdog
// surfaces as a *agentstep.StallError instead of a bare context.Canceled.
func drainReply(ctx context.Context, sess agentcli.Session, out io.Writer, who string, idle time.Duration) (string, error) {
	var wd *agentstep.Watchdog
	if idle > 0 {
		wd = agentstep.NewWatchdog(idle)
		var stop func()
		ctx, stop = wd.Start(ctx)
		defer stop()
	}
	var reply strings.Builder
	for {
		select {
		case <-ctx.Done():
			if se := agentstep.StallCause(ctx); se != nil {
				return reply.String(), se
			}
			return reply.String(), ctx.Err()
		case ev, ok := <-sess.Events():
			if !ok {
				return reply.String(), nil
			}
			if wd != nil {
				wd.TouchEvent(ev)
			}
			switch ev.Kind {
			case agentcli.EventMessage:
				if ev.Text != "" {
					reply.WriteString(ev.Text)
					_, _ = io.WriteString(out, agentcli.SafeText(ev.Text)+"\n")
				}
			case agentcli.EventToolStart, agentcli.EventToolEnd:
				// Render only. The ledger row for this boundary was written
				// synchronously by the event handler before the event reached here.
				_, _ = io.WriteString(out, agentcli.FormatEvent(ev))
			case agentcli.EventFailed:
				if ev.Error != "" {
					return reply.String(), fmt.Errorf("%s", ev.Error)
				}
				return reply.String(), fmt.Errorf("%s session failed", who)
			case agentcli.EventCompleted:
				return reply.String(), nil
			}
		}
	}
}
