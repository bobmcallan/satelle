package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// singleStoryWF: backlog entry, plan+in_progress engaging, blocked park (the
// binary synthesises it from the `park:` line), done terminal — mirrors the
// process rule's engagement predicate.
var singleStoryWF = routeHalves(
	`["*"]
obligations = ["raised", "planned", "coded", "closed"]
park = { state = "blocked" }
`,
	`[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "executor"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["planned"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)

func TestSingleStorySecondEngageRefused(t *testing.T) {
	wireWithWorkflows(t, singleStoryWF)

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
	// sty_7b69954a AC1: LIVE seat → stop-request; STALE → seat release; never cancel.
	msg := err.Error()
	for _, want := range []string{
		"engagement seat",
		"stop-request",
		"seat release",
		"LIVE",
		"STALE",
		"Never cancel",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("seat-refusal message missing %q: %s", want, msg)
		}
	}

	var still workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": b.ID}), &still)
	if still.Status != workitem.StatusBacklog {
		t.Errorf("B status changed to %q despite refuse", still.Status)
	}
}

func TestSingleStorySameStoryProgressAllowed(t *testing.T) {
	wireWithWorkflows(t, singleStoryWF)

	var a workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Solo", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "in_progress"}), &a)
	if a.Status != "in_progress" {
		t.Fatalf("same-story progress blocked: %q", a.Status)
	}
}

func TestSingleStoryBlockedFreesSeat(t *testing.T) {
	wireWithWorkflows(t, singleStoryWF)

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
	// sty_7b69954a AC6: park releases the engagement seat (not only status).
	seats, serr := verb.Dispatch(context.Background(), "story-seat-list", nil)
	if serr != nil {
		t.Fatalf("story-seat-list: %v", serr)
	}
	if strings.Contains(string(seats), a.ID) {
		t.Fatalf("parked story %s still holds a seat: %s", a.ID, seats)
	}
	// B can engage while A is parked.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": b.ID, "status": "plan"}), &b)
	if b.Status != "plan" {
		t.Fatalf("B should engage while A parked: %q", b.Status)
	}
}

func TestSingleStoryCreateIntoEngagingRefused(t *testing.T) {
	wireWithWorkflows(t, singleStoryWF)

	var a workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "A", "category": "feature", "status": "in_progress"}), &a)
	if a.Status != "in_progress" {
		t.Fatalf("first create engage: %q", a.Status)
	}
	_, err := dispatchRaw(t, "story-create", map[string]any{"title": "B", "category": "feature", "status": "plan"})
	if err == nil || !strings.Contains(err.Error(), "one performing story") {
		t.Fatalf("expected create-into-engage refused, got err=%v", err)
	}
	// sty_7b69954a AC1: second call site (create-path probe) must name stop-request.
	msg := err.Error()
	for _, want := range []string{"stop-request", "seat release", "LIVE", "STALE"} {
		if !strings.Contains(msg, want) {
			t.Errorf("create-path seat refusal missing %q: %s", want, msg)
		}
	}
}

func TestSingleStoryDefaultIsEnforce(t *testing.T) {
	wireWithWorkflows(t, singleStoryWF)

	var a, b workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "A", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "B", "category": "feature"}), &b)
	call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"})
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": b.ID, "status": "in_progress"})
	if err == nil {
		t.Fatal("default must enforce single story")
	}
}
