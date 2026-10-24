package verb_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// One session working several stories is a local rule (sty_40853d02 AC5): a second
// story engaged by the same session, in the same tree, while the first holds its
// seat is refused from the local lease store alone. The test runs with no server
// configured and wires the only seam that names a hosted location; the refusal must
// never reach it, and says nothing of holds or takeover.
func TestSecondStoryOfTheSameSessionIsRefusedFromTheLocalSeat(t *testing.T) {
	wireWithWorkflows(t, singleStoryWF)
	t.Setenv(config.SessionEnv, "sess_one_agent")
	lookups := 0
	verb.SetLocationResolver(func() (string, string) { lookups++; return "loc_never", "" })

	var a, b workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "First", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Second", "category": "feature"}), &b)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	if lookups != 1 {
		t.Fatalf("the first engage named its location %d times, want once", lookups)
	}

	_, err := dispatchRaw(t, "story-set", map[string]any{"id": b.ID, "status": "plan"})
	if err == nil {
		t.Fatal("the second story of the same session engaged while the first held the seat")
	}
	msg := err.Error()
	for _, want := range []string{"engagement seat", "one performing story at a time", a.ID} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal lacks %q: %s", want, msg)
		}
	}
	for _, banned := range []string{"takeover", "hosted", "hold takeover"} {
		if strings.Contains(msg, banned) {
			t.Errorf("a local seat refusal must not speak of %q: %s", banned, msg)
		}
	}
	if lookups != 1 {
		t.Errorf("the refusal consulted the hosted location (%d lookups); it must come from the local seat alone", lookups)
	}

	var still workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": b.ID}), &still)
	if still.Status != workitem.StatusBacklog {
		t.Errorf("the refused story moved to %q", still.Status)
	}
}
