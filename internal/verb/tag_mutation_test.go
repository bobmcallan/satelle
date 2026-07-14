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

// Repeated-key multi-value (sty_f7115cd2): add/remove within one namespace
// round-trips; list --tag ANY-matches.
func TestMultiValueNamespaceRoundTripAndListTag(t *testing.T) {
	wire(t)

	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "multi-ns",
		"tags":  []string{"epic:alpha", "order:1"},
	}), &it); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(call(t, "story-set", map[string]any{
		"id":       it.ID,
		"add_tags": []string{"epic:beta", "area:tags"},
	}), &it); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"epic:alpha", "epic:beta", "order:1", "area:tags"} {
		if !hasTag(it.Tags, want) {
			t.Errorf("missing %q in %v", want, it.Tags)
		}
	}
	// Remove one value in the namespace; the other stays.
	if err := json.Unmarshal(call(t, "story-set", map[string]any{
		"id":          it.ID,
		"remove_tags": []string{"epic:alpha"},
	}), &it); err != nil {
		t.Fatal(err)
	}
	if hasTag(it.Tags, "epic:alpha") {
		t.Errorf("epic:alpha should be gone: %v", it.Tags)
	}
	if !hasTag(it.Tags, "epic:beta") {
		t.Errorf("epic:beta should remain: %v", it.Tags)
	}

	var listed []workitem.Item
	if err := json.Unmarshal(call(t, "story-list", map[string]any{"tag": "epic:beta"}), &listed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range listed {
		if s.ID == it.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("story-list tag=epic:beta did not include %s: %+v", it.ID, listed)
	}
}
