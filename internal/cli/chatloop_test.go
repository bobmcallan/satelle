package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/verb"
)

// fakeSess mirrors a transport: every event goes through onEvent SYNCHRONOUSLY
// (the Request.OnEvent path the transcript sink hangs on) before it is queued
// on the buffered Events() channel the render loop drains.
type fakeSess struct {
	turns    []agentcli.Turn
	events   chan agentcli.Event
	onEvent  agentcli.EventHandler
	captured []byte
}

func newFakeSess() *fakeSess {
	return &fakeSess{events: make(chan agentcli.Event, 16)}
}

func (f *fakeSess) emit(ev agentcli.Event) {
	if f.onEvent != nil {
		f.onEvent(ev)
	}
	f.events <- ev
}

func (f *fakeSess) Send(_ context.Context, turn agentcli.Turn) error {
	f.turns = append(f.turns, turn)
	reply := "reply:" + strings.TrimSpace(turn.Text)
	if i := strings.LastIndex(reply, "\n"); i >= 0 {
		reply = "reply:" + strings.TrimSpace(reply[i+1:])
	}
	if strings.Contains(turn.Text, "hello") {
		f.emit(agentcli.Event{Kind: agentcli.EventToolStart, Tool: "read_file", Status: "running"})
		f.emit(agentcli.Event{Kind: agentcli.EventToolEnd, Tool: "read_file", Status: "done"})
	}
	f.emit(agentcli.Event{Kind: agentcli.EventMessage, Text: reply})
	f.emit(agentcli.Event{Kind: agentcli.EventCompleted})
	return nil
}
func (f *fakeSess) Events() <-chan agentcli.Event { return f.events }
func (f *fakeSess) Cancel() error                 { return nil }
func (f *fakeSess) Close() error                  { return nil }
func (f *fakeSess) Captured() []byte              { return f.captured }

type memLedger struct {
	msgs []verb.AgentMessage
	inv  []map[string]string
}

func (m *memLedger) WriteMessage(from, to, cc, body string) error {
	m.msgs = append(m.msgs, verb.AgentMessage{From: from, To: to, Cc: cc, Body: body, CreatedAt: time.Now()})
	return nil
}
func (m *memLedger) WriteInvocation(tool, kind, decision, decidedBy string) error {
	m.inv = append(m.inv, map[string]string{"tool": tool, "kind": kind, "decision": decision, "by": decidedBy})
	return nil
}
func (m *memLedger) ListMessagesSince(since time.Time, to string) ([]verb.AgentMessage, error) {
	var out []verb.AgentMessage
	for _, msg := range m.msgs {
		if !since.IsZero() && !msg.CreatedAt.After(since) {
			continue
		}
		if to != "" && msg.To != to && msg.To != "*" {
			continue
		}
		out = append(out, msg)
	}
	return out, nil
}

func (m *memLedger) invocationKeys() map[string]bool {
	got := map[string]bool{}
	for _, inv := range m.inv {
		got[inv["tool"]+"|"+inv["decision"]+"|"+inv["by"]] = true
	}
	return got
}

// wireLoop builds a chatLoop over a fake session with the transcript sink
// installed the way cmd_story_chat installs it (OpenOrchestrator → req.OnEvent).
func wireLoop(sess *fakeSess, led *memLedger, seat func() (seatInfo, bool, error), ask func(agentcli.PermissionRequest) bool) *chatLoop {
	loop := &chatLoop{Sess: sess, Ledger: led, StoryID: "sty_x", Seat: seat, Ask: ask}
	sess.onEvent = loop.EventHandler()
	return loop
}

func TestChatLoopSendsTurnAndPrintsReply(t *testing.T) {
	sess := newFakeSess()
	led := &memLedger{}
	loop := wireLoop(sess, led, nil, nil)
	in := strings.NewReader("hello\n/quit\n")
	var out bytes.Buffer
	if err := loop.Run(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	if len(sess.turns) != 1 || sess.turns[0].Text != "hello" {
		t.Fatalf("turns = %#v", sess.turns)
	}
	if !strings.Contains(out.String(), "reply:hello") {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(out.String(), "tool=read_file") {
		t.Fatalf("tool boundary should render: %q", out.String())
	}
}

// TestChatLedgerMatchesTranscript (AC4): one scripted conversation — a human
// line, a tool the orchestrator ran without asking (pre-allowed), the reply, one
// read tool the policy allowed, one mutator the policy denied — and the ledger
// rows that must exist for each, in order for the turn rows.
func TestChatLedgerMatchesTranscript(t *testing.T) {
	sess := newFakeSess()
	led := &memLedger{}
	loop := wireLoop(sess, led,
		func() (seatInfo, bool, error) {
			return seatInfo{ItemID: "sty_x", Engaged: true, EditCapable: false, StoryStatus: "plan"}, true, nil
		},
		func(agentcli.PermissionRequest) bool { return true },
	)
	in := strings.NewReader("hello\n/quit\n")
	var out bytes.Buffer
	if err := loop.Run(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	pol := loop.policy()
	if !pol(agentcli.PermissionRequest{ToolName: "Read", Kind: "read"}).Allow {
		t.Fatal("read tool should allow")
	}
	if pol(agentcli.PermissionRequest{ToolName: "Edit", Kind: "edit"}).Allow {
		t.Fatal("edit in plan should deny")
	}
	if len(led.msgs) != 2 {
		t.Fatalf("want exactly human+orchestrator turn rows, got %#v", led.msgs)
	}
	if led.msgs[0].From != "human" || led.msgs[0].To != "orchestrator" || led.msgs[0].Body != "hello" {
		t.Fatalf("human row = %#v", led.msgs[0])
	}
	if led.msgs[1].From != "orchestrator" || led.msgs[1].To != "human" || !strings.Contains(led.msgs[1].Body, "reply:hello") {
		t.Fatalf("agent row = %#v", led.msgs[1])
	}
	got := led.invocationKeys()
	for _, want := range []string{
		"read_file|start|session", // tool call: start boundary
		"read_file|end|session",   // tool call: end boundary
		"Read|allow|policy",       // permission decision, satelle
		"Edit|deny|policy",        // permission decision, satelle (no human ask)
	} {
		if !got[want] {
			t.Errorf("missing invocation %s in %#v", want, led.inv)
		}
	}
	// Tool rows are written by the synchronous sink, never by the render loop:
	// the two read_file rows must be present even if drain had never run.
	if n := len(led.inv); n != 4 {
		t.Fatalf("invocation rows = %d (%#v), want 4", n, led.inv)
	}
}

// TestChatLedgerHumanDecisions (AC3/AC4): in an editable state a mutator ask
// reaches the human, and both answers are ledgered as decided_by human.
func TestChatLedgerHumanDecisions(t *testing.T) {
	sess := newFakeSess()
	led := &memLedger{}
	loop := wireLoop(sess, led,
		func() (seatInfo, bool, error) {
			return seatInfo{ItemID: "sty_x", Engaged: true, EditCapable: true, StoryStatus: "in_progress"}, true, nil
		},
		func(req agentcli.PermissionRequest) bool { return req.ToolName == "Edit" },
	)
	pol := loop.policy()
	if !pol(agentcli.PermissionRequest{ToolName: "Edit", Kind: "edit"}).Allow {
		t.Fatal("human allowed Edit")
	}
	if pol(agentcli.PermissionRequest{ToolName: "Write", Kind: "edit"}).Allow {
		t.Fatal("human denied Write")
	}
	got := led.invocationKeys()
	for _, want := range []string{"Edit|allow|human", "Write|deny|human"} {
		if !got[want] {
			t.Errorf("missing invocation %s in %#v", want, led.inv)
		}
	}
}

// TestChatLoopLedgersConfiguredRoles (sty_a0372443 AC2): with --from
// developer-agent --agent reviewer, the driving turn is ledgered
// developer-agent → reviewer and the reply reviewer → developer-agent.
func TestChatLoopLedgersConfiguredRoles(t *testing.T) {
	sess := newFakeSess()
	led := &memLedger{}
	loop := wireLoop(sess, led, nil, nil)
	loop.From, loop.To = "developer-agent", "reviewer"
	var out bytes.Buffer
	if err := loop.Run(context.Background(), strings.NewReader("why did you reject\n/quit\n"), &out); err != nil {
		t.Fatal(err)
	}
	if len(led.msgs) != 2 {
		t.Fatalf("msgs = %#v, want the turn and the reply", led.msgs)
	}
	if led.msgs[0].From != "developer-agent" || led.msgs[0].To != "reviewer" {
		t.Errorf("turn row = %s -> %s", led.msgs[0].From, led.msgs[0].To)
	}
	if led.msgs[1].From != "reviewer" || led.msgs[1].To != "developer-agent" {
		t.Errorf("reply row = %s -> %s", led.msgs[1].From, led.msgs[1].To)
	}
	if !strings.Contains(out.String(), "reply:why did you reject") {
		t.Errorf("stdout = %q", out.String())
	}
}

// TestChatLoopDefaultsRolesWhenUnset: an unwired loop still ledgers the
// historical human → orchestrator pair, so the no-flag path is unchanged.
func TestChatLoopDefaultsRolesWhenUnset(t *testing.T) {
	sess := newFakeSess()
	led := &memLedger{}
	loop := wireLoop(sess, led, nil, nil)
	var out bytes.Buffer
	if err := loop.Run(context.Background(), strings.NewReader("hi\n/quit\n"), &out); err != nil {
		t.Fatal(err)
	}
	if led.msgs[0].From != "human" || led.msgs[0].To != "orchestrator" {
		t.Fatalf("turn row = %s -> %s, want human -> orchestrator", led.msgs[0].From, led.msgs[0].To)
	}
	if led.msgs[1].From != "orchestrator" || led.msgs[1].To != "human" {
		t.Fatalf("reply row = %s -> %s", led.msgs[1].From, led.msgs[1].To)
	}
}

// TestChatInboxKeysOnConfiguredRoles (sty_a0372443 AC3): the inbox fetch asks
// for the CHOSEN binding's address set, and the loop's own --from rows are not
// re-rendered back at it.
func TestChatInboxKeysOnConfiguredRoles(t *testing.T) {
	sess := newFakeSess()
	led := &memLedger{}
	loop := wireLoop(sess, led, nil, nil)
	loop.From, loop.To = "developer-agent", "reviewer"
	var out bytes.Buffer
	if err := loop.Run(context.Background(), strings.NewReader("one\n"), &out); err != nil {
		t.Fatal(err)
	}
	// One row for the reviewer, one for the orchestrator only.
	if err := led.WriteMessage("executor", "reviewer", "", "note-for-reviewer"); err != nil {
		t.Fatal(err)
	}
	led.msgs[len(led.msgs)-1].CreatedAt = time.Now().Add(time.Second)
	if err := led.WriteMessage("executor", "orchestrator", "", "note-for-console"); err != nil {
		t.Fatal(err)
	}
	led.msgs[len(led.msgs)-1].CreatedAt = time.Now().Add(2 * time.Second)
	if err := loop.Run(context.Background(), strings.NewReader("two\n/quit\n"), &out); err != nil {
		t.Fatal(err)
	}
	second := sess.turns[1].Text
	if !strings.Contains(second, "note-for-reviewer") {
		t.Errorf("second turn missing the reviewer inbox row: %q", second)
	}
	if strings.Contains(second, "note-for-console") {
		t.Errorf("a message addressed to another role was delivered: %q", second)
	}
	if strings.Contains(second, "from developer-agent") {
		t.Errorf("the loop's own turns must not be re-rendered as inbox: %q", second)
	}
}

func TestChatDeliversMessagesOnNextTurn(t *testing.T) {
	sess := newFakeSess()
	led := &memLedger{}
	loop := wireLoop(sess, led, nil, nil)
	// First turn.
	in := strings.NewReader("one\n")
	var out bytes.Buffer
	if err := loop.Run(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	if err := led.WriteMessage("executor", "orchestrator", "", "mid-session-note"); err != nil {
		t.Fatal(err)
	}
	led.msgs[len(led.msgs)-1].CreatedAt = time.Now().Add(time.Second)
	in2 := strings.NewReader("two\n/quit\n")
	if err := loop.Run(context.Background(), in2, &out); err != nil {
		t.Fatal(err)
	}
	if len(sess.turns) != 2 {
		t.Fatalf("turns = %#v", sess.turns)
	}
	if !strings.Contains(sess.turns[1].Text, "mid-session-note") {
		t.Fatalf("second turn missing inbox: %q", sess.turns[1].Text)
	}
	if strings.Contains(sess.turns[0].Text, "mid-session-note") {
		t.Fatalf("first turn unexpectedly had inbox: %q", sess.turns[0].Text)
	}
}

// TestChatInboxWatermarkAdvancesOverOwnTurns: the loop's own human/orchestrator
// rows move the watermark too, so a later turn neither re-fetches them nor
// renders an inbox header with nothing in it.
func TestChatInboxWatermarkAdvancesOverOwnTurns(t *testing.T) {
	sess := newFakeSess()
	led := &memLedger{}
	loop := wireLoop(sess, led, nil, nil)
	var out bytes.Buffer
	if err := loop.Run(context.Background(), strings.NewReader("one\n"), &out); err != nil {
		t.Fatal(err)
	}
	// Second turn: the only rows since the first watermark are the loop's own.
	if err := loop.Run(context.Background(), strings.NewReader("two\n/quit\n"), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sess.turns[1].Text, "## Inbox") {
		t.Fatalf("second turn must not carry an inbox header for own rows: %q", sess.turns[1].Text)
	}
	if loop.lastMsg.IsZero() {
		t.Fatal("watermark must advance over the loop's own rows")
	}
	// The watermark is read at the START of a turn and covers the rows the
	// orchestrator's inbox fetch returns (to=orchestrator or *). After turn two
	// that includes turn one's human row; the reply is addressed to the human
	// and is not inbox traffic, so it is not expected to move the watermark.
	turnOneHuman := led.msgs[0].CreatedAt
	if loop.lastMsg.Before(turnOneHuman) {
		t.Fatalf("watermark %v behind turn-one human row %v", loop.lastMsg, turnOneHuman)
	}
}

func TestEditSeatPolicyMutatorDeniedWithoutAsk(t *testing.T) {
	asked := false
	pol := editSeatPolicy(
		func() (seatInfo, bool, error) {
			return seatInfo{ItemID: "sty_x", Engaged: true, EditCapable: false, StoryStatus: "plan"}, true, nil
		},
		func(agentcli.PermissionRequest) bool { asked = true; return true },
		nil,
	)
	got := pol(agentcli.PermissionRequest{ToolName: "Edit", Kind: "edit"})
	if got.Allow {
		t.Fatal("mutator in non-editable state must deny")
	}
	if asked {
		t.Fatal("human must not be asked")
	}
}

func TestEditSeatPolicyMutatorAsksWhenEditable(t *testing.T) {
	asked := false
	pol := editSeatPolicy(
		func() (seatInfo, bool, error) {
			return seatInfo{ItemID: "sty_x", Engaged: true, EditCapable: true}, true, nil
		},
		func(agentcli.PermissionRequest) bool { asked = true; return true },
		nil,
	)
	got := pol(agentcli.PermissionRequest{ToolName: "Edit", Kind: "edit"})
	if !got.Allow || !asked {
		t.Fatalf("allow=%v asked=%v", got.Allow, asked)
	}
}

func TestEditSeatPolicyInFlightDeniesWithoutAsk(t *testing.T) {
	asked := false
	info := seatInfo{ItemID: "sty_x", Engaged: true, EditCapable: true, InFlight: true}
	pol := editSeatPolicy(
		func() (seatInfo, bool, error) { return info, true, nil },
		func(agentcli.PermissionRequest) bool { asked = true; return true },
		nil,
	)
	got := pol(agentcli.PermissionRequest{ToolName: "Write", Kind: "edit"})
	if got.Allow || asked {
		t.Fatalf("in-flight mutator must deny without ask, allow=%v asked=%v", got.Allow, asked)
	}
	if editPermitted(info, dispatchMarker{}) {
		t.Fatal("editPermitted must be false in-flight — chat and PreToolUse share this predicate")
	}
}

func TestEditSeatPolicyReadToolAllowedWithoutAsk(t *testing.T) {
	asked := false
	pol := editSeatPolicy(
		func() (seatInfo, bool, error) {
			return seatInfo{ItemID: "sty_x", Engaged: true, EditCapable: false}, true, nil
		},
		func(agentcli.PermissionRequest) bool { asked = true; return false },
		nil,
	)
	got := pol(agentcli.PermissionRequest{ToolName: "Read", Kind: "read"})
	if !got.Allow || asked {
		t.Fatalf("read tool should allow without ask, allow=%v asked=%v", got.Allow, asked)
	}
}

func TestEditSeatPolicyDenyReasonMatchesHook(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	info := seatInfo{
		ItemID: "sty_x", Engaged: true, EditCapable: false,
		StoryStatus: "plan", StateAgent: "planner",
		EditStates: []string{"in_progress"},
	}
	hook := editPermissionDenyReason(info, now)
	if !strings.Contains(hook, "sty_x") || !strings.Contains(hook, "plan") {
		t.Fatalf("hook reason = %q", hook)
	}
	// Same seat fails editPermitted, which is what the chat policy consults.
	if editPermitted(info, dispatchMarker{}) {
		t.Fatal("plan state must not be edit-permitted")
	}
}
