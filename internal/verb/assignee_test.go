package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestCreateStampsAssigneeFromResolver(t *testing.T) {
	withWiring(t)
	wire(t)
	verb.SetAssigneeResolver(func() string { return "P" })

	var story workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "S"}), &story); err != nil {
		t.Fatal(err)
	}
	if story.Assignee != "P" {
		t.Fatalf("story assignee = %q, want P", story.Assignee)
	}
	var task workitem.Item
	if err := json.Unmarshal(call(t, "task-create", map[string]any{"title": "T"}), &task); err != nil {
		t.Fatal(err)
	}
	if task.Assignee != "P" {
		t.Fatalf("task assignee = %q, want P", task.Assignee)
	}
}

// TestEngageNoticesOtherAssignee (sty_40853d02 AC7): an assignee different from
// the caller no longer refuses an engaging move — an assignment says who is
// expected to work a story, it is not a lock. Engage succeeds and prints one
// line naming the assignee; the assignee engaging itself prints nothing.
func TestEngageNoticesOtherAssignee(t *testing.T) {
	withWiring(t)
	db := wire(t)
	verb.SetAssigneeResolver(func() string { return "P" })

	var story workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "Held"}), &story); err != nil {
		t.Fatal(err)
	}
	q := "Q"
	if _, err := db.Stories.Update(context.Background(), story.ID, workitem.UpdateInput{Assignee: &q}, time.Now()); err != nil {
		t.Fatal(err)
	}

	var err error
	out := captureStderr(t, func() {
		_, err = verb.Dispatch(context.Background(), "story-set", mustJSON(t, map[string]any{"id": story.ID, "status": "in_progress"}))
	})
	if err != nil {
		t.Fatalf("engaging a story assigned to Q as P must not be refused: %v", err)
	}
	want := "note: " + story.ID + " is assigned to Q\n"
	if !strings.Contains(out, want) {
		t.Fatalf("stderr = %q, want a line %q", out, want)
	}
	if strings.Contains(out, "allocated") || strings.Contains(out, "actor") {
		t.Fatalf("notice used forbidden word: %q", out)
	}
	var got workitem.Item
	if err := json.Unmarshal(call(t, "story-get", map[string]any{"id": story.ID}), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" || got.Assignee != "Q" {
		t.Fatalf("story = status %q assignee %q, want in_progress / Q (the assignment is left alone)", got.Status, got.Assignee)
	}
}

func TestEngageAsAssigneePrintsNoNotice(t *testing.T) {
	withWiring(t)
	db := wire(t)
	verb.SetAssigneeResolver(func() string { return "Q" })

	var story workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "Mine"}), &story); err != nil {
		t.Fatal(err)
	}
	q := "Q"
	if _, err := db.Stories.Update(context.Background(), story.ID, workitem.UpdateInput{Assignee: &q}, time.Now()); err != nil {
		t.Fatal(err)
	}
	out := captureStderr(t, func() {
		if _, err := verb.Dispatch(context.Background(), "story-set", mustJSON(t, map[string]any{"id": story.ID, "status": "in_progress"})); err != nil {
			t.Errorf("assignee Q should engage: %v", err)
		}
	})
	if strings.Contains(out, "assigned to") {
		t.Fatalf("assignee engaging printed a notice: %q", out)
	}
}

func TestEngageEmptyAssigneeStampsResolver(t *testing.T) {
	withWiring(t)
	wire(t)

	var story workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "Open"}), &story); err != nil {
		t.Fatal(err)
	}
	if story.Assignee != "" {
		t.Fatalf("create without resolver stamped %q", story.Assignee)
	}

	verb.SetAssigneeResolver(func() string { return "P" })
	var got workitem.Item
	if err := json.Unmarshal(call(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}), &got); err != nil {
		t.Fatal(err)
	}
	if got.Assignee != "P" {
		t.Fatalf("engaged assignee = %q, want P", got.Assignee)
	}
}

func TestNoCredentialCreateAndEngageLeaveEmpty(t *testing.T) {
	withWiring(t)
	wire(t)
	verb.ClearAssigneeResolver()

	var story workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "Local"}), &story); err != nil {
		t.Fatal(err)
	}
	if story.Assignee != "" {
		t.Fatalf("create assignee = %q", story.Assignee)
	}
	var got workitem.Item
	if err := json.Unmarshal(call(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}), &got); err != nil {
		t.Fatal(err)
	}
	if got.Assignee != "" {
		t.Fatalf("engaged assignee = %q, want empty", got.Assignee)
	}
}
