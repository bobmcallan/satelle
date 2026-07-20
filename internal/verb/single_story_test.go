package verb_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// singleStoryWF: backlog entry, plan+in_progress engaging, blocked park (reviewer),
// done terminal — mirrors the process rule's engagement predicate.
const singleStoryWF = `---
name: single-story-wf
type: workflow
applies_to: ["*"]
---

` + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  plan        [agent=executor]
  in_progress [agent=executor]
  blocked     [agent=reviewer]
  done        [shape=Msquare]
  backlog -> plan -> in_progress -> done
  plan -> blocked
  in_progress -> blocked
  blocked -> plan
  blocked -> in_progress
}
` + "```" + `
`

func TestSingleStorySecondEngageRefused(t *testing.T) {
	wireWithWorkflows(t, map[string]string{"single-story-wf": singleStoryWF})

	var a, b workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "First", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Second", "category": "feature"}), &b)

	// Engage A.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	if a.Status != "plan" {
		t.Fatalf("A status = %q", a.Status)
	}

	// Engaging B while A is engaged must fail.
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": b.ID, "status": "plan"})
	if err == nil || !strings.Contains(err.Error(), "one performing story") {
		t.Fatalf("expected second engage refused, got err=%v", err)
	}
	if !strings.Contains(err.Error(), a.ID) {
		t.Errorf("error should name occupying story: %v", err)
	}

	var still workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": b.ID}), &still)
	if still.Status != workitem.StatusBacklog {
		t.Errorf("B status changed to %q despite refuse", still.Status)
	}
}

func TestSingleStorySameStoryProgressAllowed(t *testing.T) {
	wireWithWorkflows(t, map[string]string{"single-story-wf": singleStoryWF})

	var a workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Solo", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "in_progress"}), &a)
	if a.Status != "in_progress" {
		t.Fatalf("same-story progress blocked: %q", a.Status)
	}
}

func TestSingleStoryBlockedFreesSeat(t *testing.T) {
	wireWithWorkflows(t, map[string]string{"single-story-wf": singleStoryWF})

	var a, b workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Parked", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Next", "category": "feature"}), &b)

	// Engage via legal edges (sty_ebd3d666), then park.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "in_progress"}), &a)
	// Park A (agent=reviewer → not engaging).
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "blocked"}), &a)
	if a.Status != "blocked" {
		t.Fatalf("park failed: %q", a.Status)
	}
	// B can engage while A is parked.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": b.ID, "status": "plan"}), &b)
	if b.Status != "plan" {
		t.Fatalf("B should engage while A parked: %q", b.Status)
	}
}

func TestSingleStoryCreateIntoEngagingRefused(t *testing.T) {
	wireWithWorkflows(t, map[string]string{"single-story-wf": singleStoryWF})

	var a workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "A", "category": "feature", "status": "in_progress"}), &a)
	if a.Status != "in_progress" {
		t.Fatalf("first create engage: %q", a.Status)
	}
	_, err := dispatchRaw(t, "story-create", map[string]any{"title": "B", "category": "feature", "status": "plan"})
	if err == nil || !strings.Contains(err.Error(), "one performing story") {
		t.Fatalf("expected create-into-engage refused, got err=%v", err)
	}
}

func TestSingleStoryDefaultIsEnforce(t *testing.T) {
	wireWithWorkflows(t, map[string]string{"single-story-wf": singleStoryWF})

	var a, b workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "A", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "B", "category": "feature"}), &b)
	call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"})
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": b.ID, "status": "in_progress"})
	if err == nil {
		t.Fatal("default must enforce single story")
	}
}
