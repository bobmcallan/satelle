package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

type stubGater struct {
	dec verb.GateDecision
}

func (g stubGater) Gate(context.Context, workitem.Item, string) (verb.GateDecision, error) {
	return g.dec, nil
}

func dispatchRaw(t *testing.T, name string, req any) (json.RawMessage, error) {
	t.Helper()
	b, _ := json.Marshal(req)
	return verb.Dispatch(context.Background(), name, b)
}

func TestStorySetGatedRejectBlocksTransition(t *testing.T) {
	wire(t)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: true, Accept: false, Notes: "no acceptance criteria", Skill: "satelle-story-done-review"}})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "in_progress"}), &it)

	_, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "done"})
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("expected reject to block the transition, got err=%v", err)
	}

	var after workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": it.ID}), &after)
	if after.Status != "in_progress" {
		t.Errorf("status changed to %q despite reject — gate did not block", after.Status)
	}
}

func TestStorySetGatedAcceptEnacts(t *testing.T) {
	wire(t)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: true, Accept: true, Skill: "satelle-story-done-review"}})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "in_progress"}), &it)

	var after workitem.Item
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "done"}), &after)
	if after.Status != "done" {
		t.Errorf("accept should enact: status = %q, want done", after.Status)
	}
}

func TestStorySetUngatedTransitionEnacts(t *testing.T) {
	wire(t)
	// No gater wired — the gateless baseline: transitions enact directly.
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "open"}), &it)

	var after workitem.Item
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "done"}), &after)
	if after.Status != "done" {
		t.Errorf("ungated transition should enact: status = %q", after.Status)
	}
}
