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

func TestGatePayloadIncludesMessages(t *testing.T) {
	g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true})
	long := strings.Repeat("x", messagesBodyCeiling+50)
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
	var wrap struct {
		Messages []MessageState `json:"messages"`
	}
	if err := json.Unmarshal([]byte(r.got.Payload), &wrap); err != nil {
		t.Fatal(err)
	}
	if len(wrap.Messages) != messagesCount {
		t.Errorf("len(messages) = %d, want %d", len(wrap.Messages), messagesCount)
	}
	if !strings.Contains(wrap.Messages[0].Body, "[truncated]") {
		t.Errorf("over-long body not excerpted: %q", wrap.Messages[0].Body[:40])
	}
}
