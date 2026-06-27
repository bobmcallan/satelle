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

type createStub struct {
	dec verb.GateDecision
}

func (c createStub) ReviewCreate(context.Context, verb.CreateDraft) (verb.GateDecision, error) {
	return c.dec, nil
}

func TestStoryCreateGatedRejectBlocksPersist(t *testing.T) {
	wire(t)
	verb.SetCreateReviewer(createStub{dec: verb.GateDecision{Gated: true, Accept: false, Notes: "add numbered acceptance criteria", Skill: "satelle-story-structure-review"}})
	t.Cleanup(func() { verb.SetCreateReviewer(nil) })

	_, err := dispatchRaw(t, "story-create", map[string]any{"title": "vague"})
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("expected create to be rejected, got err=%v", err)
	}

	var items []workitem.Item
	json.Unmarshal(call(t, "story-list", map[string]any{}), &items)
	if len(items) != 0 {
		t.Errorf("rejected draft was persisted: %d items", len(items))
	}
}

func TestStoryCreateGatedAcceptPersists(t *testing.T) {
	wire(t)
	verb.SetCreateReviewer(createStub{dec: verb.GateDecision{Gated: true, Accept: true}})
	t.Cleanup(func() { verb.SetCreateReviewer(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "well formed", "acceptance_criteria": "1. works"}), &it)
	if it.ID == "" {
		t.Error("accepted draft should persist with an id")
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
