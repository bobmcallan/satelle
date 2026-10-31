package verb_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
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

// limitOne wires [engagement] limit = 1: one performing story at a time.
func limitOne(t *testing.T) {
	t.Helper()
	verb.SetEngagementMode(config.Config{Engagement: config.EngagementConfig{Limit: 1}})
}

// sty_9c782f0e AC4: under [engagement] limit = 1 a second story's engaging move
// is refused naming the performing one and how to proceed; nothing moves.
func TestSingleStorySecondEngageRefused(t *testing.T) {
	wireWithWorkflows(t, singleStoryWF)
	limitOne(t)

	var a, b workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "First", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Second", "category": "feature"}), &b)

	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	if a.Status != "plan" {
		t.Fatalf("A status = %q", a.Status)
	}

	_, err := dispatchRaw(t, "story-set", map[string]any{"id": b.ID, "status": "plan"})
	if err == nil {
		t.Fatal("expected second engage refused under limit = 1")
	}
	for _, want := range []string{a.ID, "[engagement] limit = 1", "park", "Never cancel"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("limit refusal missing %q: %v", want, err)
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
	limitOne(t)

	var a workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Solo", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "in_progress"}), &a)
	if a.Status != "in_progress" {
		t.Fatalf("same-story progress blocked: %q", a.Status)
	}
}

// Parking a story stops it performing, so under limit = 1 the next one engages.
func TestSingleStoryParkStopsPerforming(t *testing.T) {
	wireWithWorkflows(t, singleStoryWF)
	limitOne(t)

	var a, b workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Parked", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Next", "category": "feature"}), &b)

	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "in_progress"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "blocked"}), &a)
	if a.Status != "blocked" {
		t.Fatalf("park failed: %q", a.Status)
	}
	if isPerforming(t, a.ID) {
		t.Fatalf("parked story %s still performing", a.ID)
	}
	json.Unmarshal(call(t, "story-set", map[string]any{"id": b.ID, "status": "plan"}), &b)
	if b.Status != "plan" {
		t.Fatalf("B should engage while A parked: %q", b.Status)
	}
}

// recoverStoryWF is singleStoryWF plus the declared plan → backlog recover edge.
var recoverStoryWF = map[string]string{
	"step": singleStoryWF["step"],
	"done": strings.Replace(singleStoryWF["done"], "park = { state = \"blocked\" }\n",
		"park = { state = \"blocked\" }\nrecover = { step = \"backlog\", from = [\"plan\"] }\n", 1),
}

// A declared move back from a performing state to a non-performing, non-exit
// one (plan → backlog) ends the engagement: under limit = 1 the next story
// engages with nothing to release.
func TestSingleStoryBackToEntryStopsPerforming(t *testing.T) {
	wireWithWorkflows(t, recoverStoryWF)
	limitOne(t)

	var a, b workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Returned", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Next", "category": "feature"}), &b)

	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "backlog"}), &a)
	if a.Status != "backlog" {
		t.Fatalf("move back failed: %q", a.Status)
	}
	if isPerforming(t, a.ID) {
		t.Fatalf("story %s returned to backlog still performing", a.ID)
	}
	json.Unmarshal(call(t, "story-set", map[string]any{"id": b.ID, "status": "plan"}), &b)
	if b.Status != "plan" {
		t.Fatalf("B should engage after A returned to backlog: %q", b.Status)
	}
}

// The create path counts the same way: under limit = 1 a create straight into
// a performing state is refused while another story performs.
func TestSingleStoryCreateIntoEngagingRefused(t *testing.T) {
	wireWithWorkflows(t, singleStoryWF)
	limitOne(t)

	var a workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "A", "category": "feature", "status": "in_progress"}), &a)
	if a.Status != "in_progress" {
		t.Fatalf("first create engage: %q", a.Status)
	}
	_, err := dispatchRaw(t, "story-create", map[string]any{"title": "B", "category": "feature", "status": "plan"})
	if err == nil || !strings.Contains(err.Error(), "[engagement] limit = 1") || !strings.Contains(err.Error(), a.ID) {
		t.Fatalf("expected create-into-engage refused by the limit naming %s, got err=%v", a.ID, err)
	}
}

// sty_9c782f0e AC4: with no [engagement] limit any number of stories perform.
func TestEngagementDefaultIsUnlimited(t *testing.T) {
	wireWithWorkflows(t, singleStoryWF)

	var a, b workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "A", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "B", "category": "feature"}), &b)
	call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"})
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": b.ID, "status": "plan"}); err != nil {
		t.Fatalf("the default must admit a second performing story: %v", err)
	}
	if !isPerforming(t, a.ID) || !isPerforming(t, b.ID) {
		t.Error("both stories must be performing")
	}
}
