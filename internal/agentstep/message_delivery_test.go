package agentstep

import (
	"context"
	"errors"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// stateResolver mirrors verb.MessagesSince over a fixed set: the named
// addresses union "*", and each row keeps the state it was folded to.
func stateResolver(all []MessageState) func(context.Context, string, []string) []MessageState {
	return func(_ context.Context, _ string, addrs []string) []MessageState {
		want := map[string]bool{"*": true}
		for _, a := range addrs {
			want[a] = true
		}
		var out []MessageState
		for _, m := range all {
			if want[m.To] {
				out = append(out, m)
			}
		}
		return out
	}
}

type deliveredCall struct {
	item string
	ids  []string
}

var boardFixture = []MessageState{
	{ID: "msg_00000001", From: "orchestrator", To: "planner", Body: "decision one", State: "waiting"},
	{ID: "msg_00000002", From: "orchestrator", To: "planner", Body: "decision two", State: "waiting"},
	{ID: "msg_00000003", From: "orchestrator", To: "planner", Body: "already there", State: "delivered"},
	{ID: "msg_00000004", From: "orchestrator", To: "*", Body: "everyone", State: "waiting"},
	{ID: "msg_00000005", From: "orchestrator", To: "reviewer", Body: "not for the planner", State: "waiting"},
}

// sty_73c9aa10 AC5: a performer's own dispatch marks each role-addressed
// message it injects delivered — once, only those still waiting — and a message
// for another role is neither injected nor marked.
func TestPerformerPayloadMarksInjectedMessagesDelivered(t *testing.T) {
	g, _ := newEngine(t, "", fakeDocs{})
	g.SetMessagesResolver(stateResolver(boardFixture))
	var calls []deliveredCall
	g.SetMessagesDelivered(func(_ context.Context, itemID string, ids []string) error {
		calls = append(calls, deliveredCall{itemID, ids})
		return nil
	})
	tp, _ := g.performerPayload(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "plan", "plan", "planner", config.AgentBinding{}, "")
	if len(tp.Messages) != 4 {
		t.Fatalf("injected %d messages, want the planner's three and the broadcast", len(tp.Messages))
	}
	if len(calls) != 1 || calls[0].item != "sty_1" {
		t.Fatalf("delivered calls = %+v, want one for sty_1", calls)
	}
	want := []string{"msg_00000001", "msg_00000002", "msg_00000004"}
	if got := calls[0].ids; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("marked %v, want %v (waiting ones only, the reviewer's absent)", got, want)
	}
}

// A reviewer reading a relay is not the addressed performer receiving its
// step's message: a gate dispatch over the same messages marks nothing.
func TestReviewerPayloadLeavesMessagesWaiting(t *testing.T) {
	g, _ := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	g.SetMessagesResolver(stateResolver(boardFixture))
	calls := 0
	g.SetMessagesDelivered(func(context.Context, string, []string) error { calls++; return nil })
	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_gate", Status: "in_progress"}, "done"); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Errorf("a gate dispatch marked messages delivered (%d calls)", calls)
	}
}

// A hand-opened chat previews its binding's messages without receiving them.
func TestChatPayloadDoesNotDeliver(t *testing.T) {
	g, _ := newEngine(t, "", fakeDocs{})
	g.SetMessagesResolver(stateResolver(boardFixture))
	calls := 0
	g.SetMessagesDelivered(func(context.Context, string, []string) error { calls++; return nil })
	tp, err := g.ChatPayload(context.Background(), workitem.Item{ID: "sty_1", Status: "plan"}, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if len(tp.Messages) == 0 {
		t.Fatal("the chat payload carried no messages; the test proves nothing")
	}
	if calls != 0 {
		t.Errorf("a chat payload marked messages delivered (%d calls)", calls)
	}
}

// A mark that cannot be written never fails the step it rides on; an unwired
// writer leaves the payload exactly as it was.
func TestDeliveryFailureDoesNotFailDispatch(t *testing.T) {
	g, _ := newEngine(t, "", fakeDocs{})
	g.SetMessagesResolver(stateResolver([]MessageState{{ID: "msg_00000001", To: "planner", Body: "x", State: "waiting"}}))
	calls := 0
	g.SetMessagesDelivered(func(context.Context, string, []string) error {
		calls++
		return errors.New("ledger unavailable")
	})
	tp, _ := g.performerPayload(context.Background(), workitem.Item{ID: "sty_1"}, "plan", "plan", "planner", config.AgentBinding{}, "")
	if len(tp.Messages) != 1 || calls != 1 {
		t.Fatalf("messages = %d, delivered attempts = %d; a failed mark must still deliver the message", len(tp.Messages), calls)
	}
	g2, _ := newEngine(t, "", fakeDocs{})
	g2.SetMessagesResolver(stateResolver([]MessageState{{ID: "msg_00000001", To: "planner", Body: "x", State: "waiting"}}))
	if tp, _ := g2.performerPayload(context.Background(), workitem.Item{ID: "sty_1"}, "plan", "plan", "planner", config.AgentBinding{}, ""); len(tp.Messages) != 1 {
		t.Errorf("messages = %d without a delivered writer", len(tp.Messages))
	}
}
