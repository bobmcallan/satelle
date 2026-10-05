package agentstep

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestPayloadUnchangedWithoutMessages(t *testing.T) {
	tp := transitionPayload{Story: workitem.Item{ID: "sty_none"}, From: "backlog", To: "plan"}
	b, err := json.Marshal(tp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "messages") {
		t.Fatalf("message-free payload must omit messages key:\n%s", b)
	}
}

// sty_36ac4319 AC3: a body up to the budget reaches the performer whole; a longer
// one (cut on a rune boundary) is marked truncated and names the command that
// fetches the rest.
func TestMessageBodyBudget(t *testing.T) {
	whole := strings.Repeat("a", messageBodyBudget)
	// "é" is two bytes, so the cut lands inside a rune and must back off.
	over := strings.Repeat("a", messageBodyBudget-1) + "é" + strings.Repeat("b", 10)
	g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	g.SetMessagesResolver(func(context.Context, string, []string) []MessageState {
		return []MessageState{
			{ID: "m1", From: "orchestrator", To: "planner", Body: whole, CreatedAt: "2026-09-28T12:54:00Z"},
			{ID: "m2", From: "orchestrator", To: "planner", Body: over, CreatedAt: "2026-09-28T12:55:00Z"},
		}
	})
	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_budget", Status: "in_progress"}, "done"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.got.Payload, over) {
		t.Error("the work item must not carry the over-long body")
	}
	var kept []MessageState
	if err := json.Unmarshal(openedAt(t, r.opened, r.got.Payload, "messages"), &kept); err != nil {
		t.Fatal(err)
	}
	if len(kept) != 2 {
		t.Fatalf("messages file = %d, want 2", len(kept))
	}
	if kept[0].Body != whole || kept[0].Truncated {
		t.Errorf("the file must keep a body of exactly the budget whole (len %d, truncated %v)", len(kept[0].Body), kept[0].Truncated)
	}
	if kept[1].Body != over || kept[1].Truncated {
		t.Error("the file must keep the over-long body whole")
	}
	stdin := cappedGateStdin(t, testWorkflow, workitem.Item{ID: "sty_budget", Status: "in_progress"}, "done", func(g *Engine) {
		g.SetMessagesResolver(func(context.Context, string, []string) []MessageState {
			return []MessageState{
				{ID: "m1", From: "orchestrator", To: "planner", Body: whole, CreatedAt: "2026-09-28T12:54:00Z"},
				{ID: "m2", From: "orchestrator", To: "planner", Body: over, CreatedAt: "2026-09-28T12:55:00Z"},
			}
		})
	})
	var wrap struct {
		Messages []MessageState `json:"messages"`
	}
	if err := json.Unmarshal([]byte(stdin), &wrap); err != nil {
		t.Fatal(err)
	}
	if len(wrap.Messages) != 2 {
		t.Fatalf("check messages = %d, want 2", len(wrap.Messages))
	}
	if m := wrap.Messages[0]; m.Body != whole || m.Truncated {
		t.Errorf("a body of exactly the budget must arrive whole on the check stdin (len %d, truncated %v)", len(m.Body), m.Truncated)
	}
	m := wrap.Messages[1]
	if !m.Truncated {
		t.Fatal("a body over the budget must be marked truncated on the check stdin")
	}
	if !strings.HasPrefix(m.Body, strings.Repeat("a", messageBodyBudget-1)+"…") {
		t.Errorf("the cut must back off the split rune: %q", m.Body[len(m.Body)-120:])
	}
	for _, want := range []string{"satelle story messages sty_budget", "--to planner", "--since 2026-09-28T12:55:00Z"} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("fetch hint missing %q in %q", want, m.Body[messageBodyBudget-1:])
		}
	}
}

func TestGatePayloadIncludesMessages(t *testing.T) {
	g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	long := strings.Repeat("x", messageBodyBudget+50)
	var many []MessageState
	for i := 0; i < messagesCount+5; i++ {
		many = append(many, MessageState{
			ID:        "m" + strings.Repeat("0", 2) + string(rune('a'+i%26)),
			From:      "orchestrator",
			To:        "reviewer",
			Body:      "body-" + strings.Repeat("n", i+1),
			CreatedAt: "2026-09-09T00:00:00Z",
		})
	}
	many[0].Body = long
	g.SetMessagesResolver(func(_ context.Context, itemID string, addrs []string) []MessageState {
		if itemID != "sty_msg" {
			t.Errorf("itemID = %q", itemID)
		}
		hasReviewer := false
		for _, a := range addrs {
			if a == "reviewer" {
				hasReviewer = true
			}
		}
		if !hasReviewer {
			t.Errorf("default gate addresses = %v, want reviewer", addrs)
		}
		return many
	})
	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_msg", Status: "in_progress"}, "done"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.got.Payload, `"messages"`) {
		t.Fatalf("payload missing messages:\n%s", r.got.Payload)
	}
	if strings.Contains(r.got.Payload, long) {
		t.Error("the work item must not carry the over-long body")
	}
	var kept []MessageState
	if err := json.Unmarshal(openedAt(t, r.opened, r.got.Payload, "messages"), &kept); err != nil {
		t.Fatal(err)
	}
	if len(kept) != len(many) {
		t.Errorf("messages file = %d, want every message (%d)", len(kept), len(many))
	}
	if kept[0].Body != long || kept[0].Truncated {
		t.Error("the file must keep the over-long body whole")
	}
	stdin := cappedGateStdin(t, testWorkflow, workitem.Item{ID: "sty_msg", Status: "in_progress"}, "done", func(g *Engine) {
		g.SetMessagesResolver(func(context.Context, string, []string) []MessageState { return many })
	})
	var wrap struct {
		Messages []MessageState `json:"messages"`
	}
	if err := json.Unmarshal([]byte(stdin), &wrap); err != nil {
		t.Fatal(err)
	}
	if len(wrap.Messages) != messagesCount {
		t.Errorf("check messages = %d, want %d", len(wrap.Messages), messagesCount)
	}
	if !strings.Contains(wrap.Messages[0].Body, "[truncated]") {
		t.Errorf("over-long body not excerpted on the check stdin: %q", wrap.Messages[0].Body[:40])
	}
}
