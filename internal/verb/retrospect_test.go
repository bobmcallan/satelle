package verb_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

type fakeRetro struct {
	res verb.DispatchResult
	err error
}

func (f fakeRetro) Retrospect(ctx context.Context, item workitem.Item) (verb.DispatchResult, error) {
	return f.res, f.err
}

// TestStoryRetrospectRecordsInvocation: the verb dispatches the retrospective and
// records its cost on an agent_invocation ledger entry, returning the rollup-able
// numbers (sty_b53730e2).
func TestStoryRetrospectRecordsInvocation(t *testing.T) {
	wire(t)
	verb.SetRetrospector(fakeRetro{res: verb.DispatchResult{
		Dispatched: true, Agent: "retrospective", Model: "glm-4.6", Skill: "satelle-retrospective",
		TokensTotal: 100, DurationMs: 5000, Output: "## PROPOSALS FILED\nnone",
	}})
	defer verb.SetRetrospector(nil)

	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "T", "body": "b", "acceptance": "1. x", "category": "feature"}), &created)

	var r struct {
		StoryID     string `json:"story_id"`
		TokensTotal int    `json:"tokens_total"`
	}
	json.Unmarshal(call(t, "story-retrospect", map[string]any{"id": created.ID}), &r)
	if r.StoryID != created.ID || r.TokensTotal != 100 {
		t.Fatalf("retrospect result = %+v", r)
	}

	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": created.ID, "kind": ledger.KindAgentInvocation}), &entries)
	if len(entries) == 0 {
		t.Fatal("no agent_invocation recorded for the retrospective dispatch")
	}
}

// TestStoryRetrospectNoRetrospectorErrors: with no retrospective agent wired, the
// verb refuses clearly rather than silently no-op.
func TestStoryRetrospectNoRetrospectorErrors(t *testing.T) {
	wire(t)
	verb.SetRetrospector(nil)
	body, _ := json.Marshal(map[string]any{"id": "sty_x"})
	if _, err := verb.Dispatch(context.Background(), "story-retrospect", body); err == nil {
		t.Fatal("want an error when no retrospective agent is wired")
	}
}
