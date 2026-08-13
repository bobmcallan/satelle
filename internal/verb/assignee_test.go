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
	wire(t)
	t.Cleanup(verb.ClearAssigneeResolver)
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

func TestEngageRefusesOtherHolder(t *testing.T) {
	db := wire(t)
	t.Cleanup(verb.ClearAssigneeResolver)
	verb.SetAssigneeResolver(func() string { return "P" })

	var story workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "Held"}), &story); err != nil {
		t.Fatal(err)
	}
	q := "Q"
	if _, err := db.Stories.Update(context.Background(), story.ID, workitem.UpdateInput{Assignee: &q}, time.Now()); err != nil {
		t.Fatal(err)
	}

	_, err := verb.Dispatch(context.Background(), "story-set", mustJSON(t, map[string]any{"id": story.ID, "status": "in_progress"}))
	if err == nil {
		t.Fatal("expected refuse when assigned to Q")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Q") || !strings.Contains(msg, "assigned to") {
		t.Fatalf("refuse message = %q", msg)
	}
	if strings.Contains(msg, "allocated") || strings.Contains(msg, "actor") {
		t.Fatalf("refuse used forbidden word: %q", msg)
	}

	verb.SetAssigneeResolver(func() string { return "Q" })
	if _, err := verb.Dispatch(context.Background(), "story-set", mustJSON(t, map[string]any{"id": story.ID, "status": "in_progress"})); err != nil {
		t.Fatalf("holder Q should engage: %v", err)
	}
}

func TestEngageEmptyAssigneeStampsResolver(t *testing.T) {
	wire(t)
	t.Cleanup(verb.ClearAssigneeResolver)

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
	wire(t)
	t.Cleanup(verb.ClearAssigneeResolver)
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
