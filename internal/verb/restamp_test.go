package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// fakeWorkflowResolver drives story-restamp in tests: byCategory answers
// WorkflowNameFor, and presence in states marks a workflow as resolving (its
// value is the declared lifecycle states).
type fakeWorkflowResolver struct {
	byCategory map[string]string
	states     map[string][]string
}

func (f *fakeWorkflowResolver) WorkflowNameFor(_ context.Context, category string) string {
	return f.byCategory[category]
}

func (f *fakeWorkflowResolver) WorkflowStates(_ context.Context, name string) ([]string, bool) {
	s, ok := f.states[name]
	return s, ok
}

// wireResolver wires a fake workflow resolver, resetting on cleanup.
func wireResolver(t *testing.T, f *fakeWorkflowResolver) {
	t.Helper()
	verb.SetWorkflowResolver(f)
	t.Cleanup(func() { verb.SetWorkflowResolver(nil) })
}

// dispatchErr dispatches a verb expected to FAIL and returns the error.
func dispatchErr(t *testing.T, name string, req any) error {
	t.Helper()
	b, _ := json.Marshal(req)
	_, err := verb.Dispatch(context.Background(), name, b)
	if err == nil {
		t.Fatalf("dispatch %s: expected an error", name)
	}
	return err
}

func TestStoryRestampResolvesByCategory(t *testing.T) {
	wire(t)
	wireResolver(t, &fakeWorkflowResolver{
		byCategory: map[string]string{"feature": "wf-feature", "governance": "wf-gov"},
		states: map[string][]string{
			"wf-feature": {"backlog", "in_progress", "done", "cancelled"},
			"wf-gov":     {"backlog", "in_progress", "done", "cancelled"},
		},
	})

	// Created as feature → stamped wf-feature; carries tags that must survive.
	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "Migrate the assessment", "category": "feature",
		"tags": []string{"keep-me", "estimate-minutes:30"},
	}), &created)
	if got := tagValue(created.Tags, "workflow"); got != "wf-feature" {
		t.Fatalf("create stamp = %q, want wf-feature", got)
	}

	// Re-categorised mid-flight; restamp re-resolves from the CURRENT category.
	call(t, "story-set", map[string]any{"id": created.ID, "category": "governance"})
	var restamped workitem.Item
	json.Unmarshal(call(t, "story-restamp", map[string]any{"id": created.ID}), &restamped)
	if got := tagValue(restamped.Tags, "workflow"); got != "wf-gov" {
		t.Errorf("restamp = %q, want wf-gov (tags: %v)", got, restamped.Tags)
	}
	// Every other tag survives, and the old stamp is replaced, not duplicated.
	for _, want := range []string{"keep-me", "estimate-minutes:30"} {
		if !hasTag(restamped.Tags, want) {
			t.Errorf("tag %q lost on restamp: %v", want, restamped.Tags)
		}
	}
	if n := countPrefix(restamped.Tags, "workflow:"); n != 1 {
		t.Errorf("expected exactly one workflow: stamp, got %d: %v", n, restamped.Tags)
	}

	// The trail records the re-stamp: a second workflow_stamped row, old -> new.
	var entries []map[string]any
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": created.ID}), &entries)
	var sawRestamp bool
	for _, e := range entries {
		if e["kind"] == "workflow_stamped" && strings.Contains(e["body"].(string), "re-stamped: wf-feature -> wf-gov") {
			sawRestamp = true
		}
	}
	if !sawRestamp {
		t.Errorf("no workflow_stamped re-stamp ledger row: %v", entries)
	}
}

func TestStoryRestampExplicitWorkflow(t *testing.T) {
	wire(t)
	wireResolver(t, &fakeWorkflowResolver{
		byCategory: map[string]string{"feature": "wf-feature"},
		states: map[string][]string{
			"wf-feature": {"backlog", "in_progress", "done"},
			"wf-other":   {"backlog", "done"},
		},
	})
	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "T", "category": "feature"}), &created)

	var restamped workitem.Item
	json.Unmarshal(call(t, "story-restamp", map[string]any{"id": created.ID, "workflow": "wf-other"}), &restamped)
	if got := tagValue(restamped.Tags, "workflow"); got != "wf-other" {
		t.Errorf("explicit restamp = %q, want wf-other", got)
	}
}

func TestStoryRestampValidatesTarget(t *testing.T) {
	wire(t)
	wireResolver(t, &fakeWorkflowResolver{
		byCategory: map[string]string{"feature": "wf-feature"},
		states: map[string][]string{
			"wf-feature": {"backlog", "in_progress", "done"},
			// wf-narrow does not declare backlog — a backlog story can't move there.
			"wf-narrow": {"ready", "running", "complete"},
		},
	})
	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "T", "category": "feature"}), &created)

	// Unknown workflow → rejected before anything changes.
	if err := dispatchErr(t, "story-restamp", map[string]any{"id": created.ID, "workflow": "wf-nope"}); !strings.Contains(err.Error(), "does not resolve") {
		t.Errorf("unknown workflow error = %v", err)
	}
	// Status not a state of the target → rejected, naming the states.
	err := dispatchErr(t, "story-restamp", map[string]any{"id": created.ID, "workflow": "wf-narrow"})
	if !strings.Contains(err.Error(), `status "backlog" is not a state`) || !strings.Contains(err.Error(), "ready, running, complete") {
		t.Errorf("status-mismatch error = %v", err)
	}
	// The stamp is unchanged after both rejects.
	var got workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": created.ID}), &got)
	if v := tagValue(got.Tags, "workflow"); v != "wf-feature" {
		t.Errorf("stamp changed on a rejected restamp: %q", v)
	}
}

func TestStoryRestampIsStoryOnly(t *testing.T) {
	wire(t)
	wireResolver(t, &fakeWorkflowResolver{states: map[string][]string{"wf-x": {"backlog", "done"}}})

	var task workitem.Item
	json.Unmarshal(call(t, "task-create", map[string]any{"title": "a task"}), &task)
	if err := dispatchErr(t, "story-restamp", map[string]any{"id": task.ID, "workflow": "wf-x"}); !strings.Contains(err.Error(), "story-only") {
		t.Errorf("non-story error = %v", err)
	}
}

func TestStoryRestampSameTargetIsANoop(t *testing.T) {
	wire(t)
	wireResolver(t, &fakeWorkflowResolver{
		byCategory: map[string]string{"feature": "wf-feature"},
		states:     map[string][]string{"wf-feature": {"backlog", "in_progress", "done"}},
	})
	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "T", "category": "feature"}), &created)

	call(t, "story-restamp", map[string]any{"id": created.ID})
	var entries []map[string]any
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": created.ID}), &entries)
	stamped := 0
	for _, e := range entries {
		if e["kind"] == "workflow_stamped" {
			stamped++
		}
	}
	if stamped != 1 {
		t.Errorf("same-target restamp must not add a ledger row: %d workflow_stamped rows", stamped)
	}
}

// --- small tag helpers ---

func tagValue(tags []string, key string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, key+":") {
			return strings.TrimPrefix(t, key+":")
		}
	}
	return ""
}

func countPrefix(tags []string, prefix string) int {
	n := 0
	for _, t := range tags {
		if strings.HasPrefix(t, prefix) {
			n++
		}
	}
	return n
}
