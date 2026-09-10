package verb_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestMessageCcWidensReadNotDirection is sty_8e0b29a0's addressing resolution:
// a rework relay turn must be BOTH directed (real from/to, so the transcript
// reads as the conversation it was) and readable by whoever later judges the
// edge. cc is the audience; to stays the recipient.
func TestMessageCcWidensReadNotDirection(t *testing.T) {
	wire(t)
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "cc"}), &it); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(map[string]any{"head_sha": "abc123", "dirty": false, "to": "in_progress"})
	call(t, "ledger-append", map[string]any{
		"story_id": it.ID, "kind": ledger.KindEngagementBaseline, "payload": json.RawMessage(payload),
	})
	time.Sleep(2 * time.Millisecond)
	// A relay turn: directed consult → coder, audience everyone.
	call(t, "story-message", map[string]any{
		"id": it.ID, "from": "consult", "to": "coder", "cc": "*", "body": "AC4 has no test",
	})
	// The same direction WITHOUT cc — the control for the no-op guarantee.
	call(t, "story-message", map[string]any{
		"id": it.ID, "from": "consult", "to": "coder", "body": "private aside",
	})

	// The direction survives: this is what `satelle story messages` renders.
	var all []verb.AgentMessage
	if err := json.Unmarshal(call(t, "story-messages", map[string]any{"id": it.ID}), &all); err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("rows = %d, want 2: %+v", len(all), all)
	}
	if all[0].From != "consult" || all[0].To != "coder" || all[0].Cc != "*" {
		t.Fatalf("relay row lost its direction/audience: %+v", all[0])
	}
	if all[1].Cc != "" {
		t.Errorf("cc synthesised on a row that declared none: %+v", all[1])
	}

	// The audience is what reaches an edge reviewer's payload addresses — and a
	// row with no cc stays invisible there, so the field widens read access ONLY
	// when it is set.
	got := verb.MessagesSince(context.Background(), it.ID, []string{"reviewer"})
	if len(got) != 1 || got[0].Body != "AC4 has no test" {
		t.Fatalf("reviewer sees %+v; want only the cc=* relay turn", got)
	}

	// The read surface filters the same way.
	var ccOnly []verb.AgentMessage
	if err := json.Unmarshal(call(t, "story-messages", map[string]any{"id": it.ID, "to": "reviewer"}), &ccOnly); err != nil {
		t.Fatal(err)
	}
	if len(ccOnly) != 1 || ccOnly[0].Body != "AC4 has no test" {
		t.Fatalf("to=reviewer got %+v; want only the cc=* relay turn", ccOnly)
	}
}
