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

// sty_73c9aa10 AC5: a performer's own dispatch marks each role-addressed
// message it injects delivered — once, only those still waiting — and a message
// for another role is neither injected nor marked.
func TestPerformerPayloadMarksInjectedMessagesDelivered(t *testing.T) {
	g, _ := newEngine(t, "", fakeDocs{})
	g.SetMessagesResolver(stateResolver([]MessageState{
		{ID: "msg_00000001", From: "orchestrator", To: "planner", Body: "decision one", State: "waiting"},
		{ID: "msg_00000002", From: "orchestrator", To: "planner", Body: "decision two", State: "waiting"},
		{ID: "msg_00000003", From: "orchestrator", To: "planner", Body: "already there", State: "delivered"},
		{ID: "msg_00000004", From: "orchestrator", To: "*", Body: "everyone", State: "waiting"},
		{ID: "msg_00000005", From: "orchestrator", To: "reviewer", Body: "not for the planner", State: "waiting"},
	}))
	type mark struct{ item, id, state string }
	var marks []mark
	g.SetMessageMarker(func(_ context.Context, itemID, msgID, state string) error {
		marks = append(marks, mark{itemID, msgID, state})
		return nil
	})
	tp := g.performerPayload(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "plan", "plan", "planner", config.AgentBinding{})
	if len(tp.Messages) != 4 {
		t.Fatalf("injected %d messages, want the planner's three and the broadcast", len(tp.Messages))
	}
	want := []mark{
		{"sty_1", "msg_00000001", "delivered"},
		{"sty_1", "msg_00000002", "delivered"},
		{"sty_1", "msg_00000004", "delivered"},
	}
	if len(marks) != len(want) {
		t.Fatalf("marks = %+v, want %+v", marks, want)
	}
	for i := range want {
		if marks[i] != want[i] {
			t.Errorf("mark %d = %+v, want %+v", i, marks[i], want[i])
		}
	}
}

// A mark that cannot be written never fails the step it rides on, and a payload
// built for a reader that is not the performer (a hand-opened chat) marks
// nothing — reading a message is not receiving it.
func TestMessageMarkingIsBestEffortAndPerformerOnly(t *testing.T) {
	g, _ := newEngine(t, "", fakeDocs{})
	g.SetMessagesResolver(stateResolver([]MessageState{{ID: "msg_00000001", To: "planner", Body: "x", State: "waiting"}}))
	calls := 0
	g.SetMessageMarker(func(context.Context, string, string, string) error {
		calls++
		return errors.New("ledger unavailable")
	})
	tp := g.performerPayload(context.Background(), workitem.Item{ID: "sty_1"}, "plan", "plan", "planner", config.AgentBinding{})
	if len(tp.Messages) != 1 || calls != 1 {
		t.Fatalf("messages = %d, mark attempts = %d; a failed mark must still deliver the message", len(tp.Messages), calls)
	}
	if _, err := g.ChatPayload(context.Background(), workitem.Item{ID: "sty_1", Status: "plan"}, "planner"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("a chat payload marked a message (%d attempts)", calls)
	}
	// Unwired marker: messages flow exactly as before.
	g2, _ := newEngine(t, "", fakeDocs{})
	g2.SetMessagesResolver(stateResolver([]MessageState{{ID: "msg_00000001", To: "planner", Body: "x", State: "waiting"}}))
	if tp := g2.performerPayload(context.Background(), workitem.Item{ID: "sty_1"}, "plan", "plan", "planner", config.AgentBinding{}); len(tp.Messages) != 1 {
		t.Errorf("messages = %d without a marker", len(tp.Messages))
	}
}
