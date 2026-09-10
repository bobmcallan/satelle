package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/verb"
)

// chatLedger is the transcript sink for satelle story chat. Human and agent
// turns go through story-message; tool/permission decisions are invocation rows.
type chatLedger interface {
	WriteMessage(from, to, body string) error
	WriteInvocation(tool, kind, decision, decidedBy string) error
	ListMessagesSince(since time.Time, to string) ([]verb.AgentMessage, error)
}

// Default chat identities: the driving side is a human at a prompt and the
// session is the orchestrator console, unless --from/--agent say otherwise
// (sty_a0372443).
const (
	chatDefaultFrom  = "human"
	chatDefaultTo    = orchestratorRole
	orchestratorRole = "orchestrator"
)

// chatLoop is the transport-free story-chat core (sty_1de7494c). From is the
// role the driving side speaks as (--from); To is the binding it is talking to
// (--agent). Both ledger directions and the inbox address set key on them
// rather than on the literals "human"/"orchestrator" (sty_a0372443).
type chatLoop struct {
	Sess    agentcli.Session
	StoryID string
	From    string
	To      string
	Ledger  chatLedger
	Ask     func(agentcli.PermissionRequest) bool
	Seat    func() (seatInfo, bool, error)
	Now     func() time.Time
	lastMsg time.Time
}

func (l *chatLoop) policy() agentcli.PermissionPolicy {
	ask := l.Ask
	if ask == nil {
		ask = func(agentcli.PermissionRequest) bool { return false }
	}
	seat := l.Seat
	return editSeatPolicy(seat, ask, func(req agentcli.PermissionRequest, allow bool, by string) {
		if l.Ledger == nil {
			return
		}
		dec := "deny"
		if allow {
			dec = "allow"
		}
		_ = l.Ledger.WriteInvocation(req.ToolName, req.Kind, dec, by)
	})
}

// editSeatPolicy is the two-stage chat permission gate. Satelle decides first
// using the same editPermitted predicate as PreToolUse (cmd_hook.go); only
// then is the human asked. A mutator outside an executor-owned performing
// state is denied without prompting.
func editSeatPolicy(seat func() (seatInfo, bool, error), ask func(agentcli.PermissionRequest) bool, onDecision func(agentcli.PermissionRequest, bool, string)) agentcli.PermissionPolicy {
	if onDecision == nil {
		onDecision = func(agentcli.PermissionRequest, bool, string) {}
	}
	if ask == nil {
		ask = func(agentcli.PermissionRequest) bool { return false }
	}
	return func(req agentcli.PermissionRequest) agentcli.PermissionDecision {
		if agentcli.IsMutatorRequest(req) {
			if seat != nil {
				info, _, err := seat()
				if err != nil || !editPermitted(info, dispatchMarker{}) {
					onDecision(req, false, "policy")
					return agentcli.PermissionDecision{Allow: false}
				}
			}
			if !ask(req) {
				onDecision(req, false, "human")
				return agentcli.PermissionDecision{Allow: false}
			}
			onDecision(req, true, "human")
			return agentcli.PermissionDecision{Allow: true}
		}
		onDecision(req, true, "policy")
		return agentcli.PermissionDecision{Allow: true}
	}
}

// EventHandler is the transcript's tool-row sink. It is installed as the
// session Request's OnEvent, which transports call inline from their reader
// goroutine BEFORE the buffered Events() fan-out — so every tool boundary is
// ledgered synchronously even when the terminal render (drain) drops events
// under overflow (sty_1de7494c AC4). Turn rows are written at turn boundaries
// in Run; permission rows in policy. Nothing here depends on the lossy channel.
func (l *chatLoop) EventHandler() agentcli.EventHandler {
	return func(ev agentcli.Event) {
		if l.Ledger == nil {
			return
		}
		switch ev.Kind {
		case agentcli.EventToolStart:
			_ = l.Ledger.WriteInvocation(ev.Tool, ev.Status, "start", "session")
		case agentcli.EventToolEnd:
			_ = l.Ledger.WriteInvocation(ev.Tool, ev.Status, "end", "session")
		}
	}
}

func (l *chatLoop) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	if l.Now == nil {
		l.Now = time.Now
	}
	if strings.TrimSpace(l.From) == "" {
		l.From = chatDefaultFrom
	}
	if strings.TrimSpace(l.To) == "" {
		l.To = chatDefaultTo
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for {
		if _, err := fmt.Fprint(out, "> "); err != nil {
			return err
		}
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if line == "/quit" || line == "/exit" {
			break
		}
		text := l.prependInbox(line)
		if l.Ledger != nil {
			_ = l.Ledger.WriteMessage(l.From, l.To, line)
		}
		if err := l.Sess.Send(ctx, agentcli.Turn{Text: text}); err != nil {
			return err
		}
		reply, err := l.drain(ctx, out)
		if err != nil {
			return err
		}
		if reply != "" && l.Ledger != nil {
			_ = l.Ledger.WriteMessage(l.To, l.From, reply)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return nil
}

func (l *chatLoop) prependInbox(line string) string {
	if l.Ledger == nil {
		return line
	}
	msgs, err := l.Ledger.ListMessagesSince(l.lastMsg, l.To)
	if err != nil || len(msgs) == 0 {
		return line
	}
	// The watermark advances over EVERY row seen — including the loop's own
	// From turns — so a row is fetched once whatever `--since` semantics the
	// store applies, and no turn re-renders an inbox it already delivered.
	since := l.lastMsg
	latest := l.lastMsg
	var b strings.Builder
	n := 0
	for _, m := range msgs {
		if !since.IsZero() && !m.CreatedAt.After(since) {
			continue
		}
		if m.CreatedAt.After(latest) {
			latest = m.CreatedAt
		}
		if strings.EqualFold(m.From, l.From) {
			continue
		}
		if n == 0 {
			b.WriteString("## Inbox (story messages since last turn)\n\n")
		}
		n++
		b.WriteString("- from ")
		b.WriteString(m.From)
		b.WriteString(": ")
		b.WriteString(m.Body)
		b.WriteString("\n")
	}
	l.lastMsg = latest
	if n == 0 {
		return line
	}
	b.WriteString("\n")
	b.WriteString(line)
	return b.String()
}

func (l *chatLoop) drain(ctx context.Context, out io.Writer) (string, error) {
	var reply strings.Builder
	for {
		select {
		case <-ctx.Done():
			return reply.String(), ctx.Err()
		case ev, ok := <-l.Sess.Events():
			if !ok {
				return reply.String(), nil
			}
			switch ev.Kind {
			case agentcli.EventMessage:
				if ev.Text != "" {
					reply.WriteString(ev.Text)
					_, _ = io.WriteString(out, agentcli.SafeText(ev.Text)+"\n")
				}
			case agentcli.EventToolStart, agentcli.EventToolEnd:
				// Render only. The ledger row for this boundary was written
				// synchronously by EventHandler before the event reached here.
				_, _ = io.WriteString(out, agentcli.FormatEvent(ev))
			case agentcli.EventFailed:
				if ev.Error != "" {
					return reply.String(), fmt.Errorf("%s", ev.Error)
				}
				return reply.String(), fmt.Errorf("%s session failed", l.To)
			case agentcli.EventCompleted:
				return reply.String(), nil
			}
		}
	}
}

func stdinIsInteractive(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func promptAllow(out io.Writer, in *bufio.Reader, req agentcli.PermissionRequest) bool {
	fmt.Fprintf(out, "permission: %s (kind=%s) allow/deny? ", req.ToolName, req.Kind)
	line, err := in.ReadString('\n')
	if err != nil {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "allow" || ans == "y" || ans == "yes"
}
