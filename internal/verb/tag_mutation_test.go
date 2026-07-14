package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestWorkItemSetAddTagsDoesNotClobber(t *testing.T) {
	wire(t)

	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "tag mut",
		"tags":  []string{"workflow:wf", "sprint:3", "order:1"},
	}), &it); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(call(t, "story-set", map[string]any{
		"id":       it.ID,
		"add_tags": []string{"sprint:4", "area:cli"},
	}), &it); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"workflow:wf", "sprint:3", "order:1", "sprint:4", "area:cli"} {
		if !hasTag(it.Tags, want) {
			t.Errorf("missing tag %q in %v", want, it.Tags)
		}
	}
	// Group remove sprint:*
	if err := json.Unmarshal(call(t, "story-set", map[string]any{
		"id":          it.ID,
		"remove_tags": []string{"sprint:*"},
	}), &it); err != nil {
		t.Fatal(err)
	}
	for _, tg := range it.Tags {
		if strings.HasPrefix(tg, "sprint:") {
			t.Errorf("sprint tag survived group remove: %v", it.Tags)
		}
	}
	if !hasTag(it.Tags, "workflow:wf") || !hasTag(it.Tags, "order:1") {
		t.Errorf("unrelated tags dropped: %v", it.Tags)
	}
}

func TestWorkItemSetTagsExclusiveOfAdd(t *testing.T) {
	wire(t)
	var it workitem.Item
	_ = json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "tags": []string{"a"}}), &it)
	body, _ := json.Marshal(map[string]any{
		"id":       it.ID,
		"tags":     []string{"only"},
		"add_tags": []string{"extra"},
	})
	_, err := verb.Dispatch(context.Background(), "story-set", body)
	if err == nil {
		t.Fatal("expected error combining tags + add_tags")
	}
	if !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("error = %v, want cannot be combined", err)
	}
}
