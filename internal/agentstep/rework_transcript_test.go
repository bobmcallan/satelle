package agentstep

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// countingRunner is fakeRunner plus a call count, so "the gate runs cold and
// one-shot" is an assertion rather than an assumption: one Run, no session.
type countingRunner struct {
	out   string
	calls int
	got   agentcli.Request
}

func (c *countingRunner) Name() string    { return "counting" }
func (c *countingRunner) Command() string { return "counting -p --append-system-prompt {system}" }
func (c *countingRunner) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	c.calls++
	c.got = req
	return []byte(c.out), nil
}

// TestReworkTranscriptReachesTheGateColdAndOneShot is sty_8e0b29a0 AC5.
//
// The relay ledgers each turn with its real coder/consult direction and cc="*".
// The gate payload's address set is ["reviewer", <gate agent>]; the wildcard
// audience is what carries the transcript into it (verb.MessagesSince unions
// "*" and now the cc field). Nothing about the gate changes: it is still one
// Run over a payload satelle built, with no live session anywhere.
func TestReworkTranscriptReachesTheGateColdAndOneShot(t *testing.T) {
	r := &countingRunner{out: `{"decision":"accept"}`}
	g := New(r, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true}, "/repo", "")

	// A three-round relay transcript, as the ledger holds it: directed roles,
	// wildcard audience. The resolver stands in for verb.MessagesSince, which
	// has already applied the cc union — this test asserts the ENGINE delivers
	// what the store hands it, in order, within the budget.
	transcript := []MessageState{
		{ID: "m1", From: "consult", To: "coder", Body: "NOT READY: AC4 has no test", CreatedAt: "2026-09-09T00:00:01Z"},
		{ID: "m2", From: "coder", To: "consult", Body: "added TestAC4", CreatedAt: "2026-09-09T00:00:02Z"},
		{ID: "m3", From: "consult", To: "coder", Body: "READY", CreatedAt: "2026-09-09T00:00:03Z"},
	}
	var sawAddrs []string
	g.SetMessagesResolver(func(_ context.Context, itemID string, addrs []string) []MessageState {
		sawAddrs = addrs
		return transcript
	})
	// If the engine ever tried to hold a live session for a gate, this opener
	// would be reached. It must not be.
	sessions := 0
	g.newOpener = func(string, string) (agentcli.SessionOpener, error) {
		sessions++
		return nil, nil
	}

	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_rw", Status: "in_progress"}, "done"); err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 {
		t.Errorf("gate ran %d times, want exactly one cold one-shot run", r.calls)
	}
	if sessions != 0 {
		t.Errorf("gate opened %d live sessions; a gate is never warm", sessions)
	}
	// The address set is unchanged by this story: the transcript rides in on its
	// own cc="*", not on the engine naming the relay's roles.
	for _, a := range sawAddrs {
		if a == "coder" || a == "consult" {
			t.Errorf("gate addresses leaked a relay role (%q) — the audience is the message's, not the payload builder's: %v", a, sawAddrs)
		}
	}

	var wrap struct {
		Messages []MessageState `json:"messages"`
	}
	if err := json.Unmarshal([]byte(r.got.Payload), &wrap); err != nil {
		t.Fatal(err)
	}
	if len(wrap.Messages) != len(transcript) {
		t.Fatalf("payload messages = %d, want the whole %d-turn transcript", len(wrap.Messages), len(transcript))
	}
	for i := range transcript {
		if wrap.Messages[i].From != transcript[i].From || wrap.Messages[i].To != transcript[i].To {
			t.Errorf("turn %d = %s -> %s, want %s -> %s; the reviewer must see who said what",
				i, wrap.Messages[i].From, wrap.Messages[i].To, transcript[i].From, transcript[i].To)
		}
		if wrap.Messages[i].Body != transcript[i].Body {
			t.Errorf("turn %d body = %q, want %q", i, wrap.Messages[i].Body, transcript[i].Body)
		}
	}
}

// TestReworkTranscriptRespectsTheExistingBudget: a long relay does not become a
// licence to blow the message budget. The transcript is context, and context is
// budgeted exactly as it was before the relay existed.
func TestReworkTranscriptRespectsTheExistingBudget(t *testing.T) {
	r := &countingRunner{out: `{"decision":"accept"}`}
	g := New(r, fakeDocs{workflow: testWorkflow, skillBody: "rubric", skillFound: true}, "/repo", "")

	var many []MessageState
	for i := 0; i < messagesCount+7; i++ {
		many = append(many, MessageState{
			ID: "m" + string(rune('a'+i%26)), From: "consult", To: "coder",
			Body: "NOT READY: " + strings.Repeat("y", messagesBodyCeiling+10),
		})
	}
	g.SetMessagesResolver(func(context.Context, string, []string) []MessageState { return many })

	if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_rw2", Status: "in_progress"}, "done"); err != nil {
		t.Fatal(err)
	}
	var wrap struct {
		Messages []MessageState `json:"messages"`
	}
	if err := json.Unmarshal([]byte(r.got.Payload), &wrap); err != nil {
		t.Fatal(err)
	}
	if len(wrap.Messages) != messagesCount {
		t.Errorf("payload messages = %d, want the existing cap %d", len(wrap.Messages), messagesCount)
	}
	if !strings.Contains(wrap.Messages[0].Body, "[truncated]") {
		t.Errorf("an over-long turn must be excerpted, not delivered whole")
	}
}
