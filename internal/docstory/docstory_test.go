package docstory

import (
	"context"
	"errors"
	"testing"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// qualifying is the shape reindex files: a story, non-terminal, high priority,
// carrying the marker tag and a document relationship.
func qualifying() workitem.Item {
	return workitem.Item{
		ID: "sty_diag", Kind: workitem.KindStory, Status: workitem.StatusBacklog,
		Priority: Priority, Title: "Fix workflows structure: workflows/done",
		Tags: []string{MarkerTag, Tag("workflows", "done")},
	}
}

// TestQualifiesIsBounded (sty_88d40a60 AC4) states the surface's whole
// vocabulary: exactly which stories may be surfaced, and — case by case — which
// near-misses may not, so the advisory cannot grow into noise.
func TestQualifiesIsBounded(t *testing.T) {
	if !Qualifies(qualifying()) {
		t.Fatal("the shape reindex files must qualify")
	}
	cases := []struct {
		name  string
		mutit func(*workitem.Item)
	}{
		{"a done diagnosis is history", func(it *workitem.Item) { it.Status = workitem.StatusDone }},
		{"a cancelled diagnosis is history", func(it *workitem.Item) { it.Status = "cancelled" }},
		{"a lower priority is not this class of breakage", func(it *workitem.Item) { it.Priority = "medium" }},
		{"an unprioritised story is not one either", func(it *workitem.Item) { it.Priority = "" }},
		{"a hand-written story without the marker is not the indexer's", func(it *workitem.Item) {
			it.Tags = []string{Tag("workflows", "done")}
		}},
		{"a system story about no document has nothing to point at", func(it *workitem.Item) {
			it.Tags = []string{MarkerTag}
		}},
		{"a malformed doc tag is not a document relationship", func(it *workitem.Item) {
			it.Tags = []string{MarkerTag, "doc:workflows"}
		}},
		{"an empty doc name is not one either", func(it *workitem.Item) {
			it.Tags = []string{MarkerTag, "doc:workflows/"}
		}},
		{"a task carries no lifecycle to block", func(it *workitem.Item) { it.Kind = workitem.KindTask }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			it := qualifying()
			c.mutit(&it)
			if Qualifies(it) {
				t.Errorf("must not qualify: %+v", it)
			}
		})
	}
}

// TestOpenAndForDoc covers the read path: qualifying stories come back with the
// document parsed out of the tag, and ForDoc finds the one for a named document.
func TestOpenAndForDoc(t *testing.T) {
	items := []workitem.Item{
		qualifying(),
		{ID: "sty_step", Kind: workitem.KindStory, Status: "plan", Priority: Priority,
			Tags: []string{MarkerTag, Tag("workflows", "step")}},
		{ID: "sty_noise", Kind: workitem.KindStory, Status: workitem.StatusBacklog, Priority: "low",
			Tags: []string{MarkerTag, Tag("skills", "whatever")}},
	}
	list := func(context.Context, workitem.ListFilter) ([]workitem.Item, error) { return items, nil }

	refs := Open(context.Background(), list)
	if len(refs) != 2 {
		t.Fatalf("Open = %+v, want the two qualifying stories", refs)
	}
	got, ok := ForDoc(refs, "workflows", "done")
	if !ok || got.ID != "sty_diag" || got.Doc() != "workflows/done" || got.Title == "" {
		t.Errorf("ForDoc(workflows/done) = (%+v, %v)", got, ok)
	}
	if id := IDForDoc(refs, "workflows", "step"); id != "sty_step" {
		t.Errorf("IDForDoc(workflows/step) = %q", id)
	}
	// A document with no open story names nothing — the silence a sound repo gets.
	if _, ok := ForDoc(refs, "workflows", "absent"); ok {
		t.Error("ForDoc must not invent a story for an untracked document")
	}
	if id := IDForDoc(refs, "skills", "whatever"); id != "" {
		t.Errorf("a low-priority story must not be surfaced, got %q", id)
	}
}

// TestOpenIsSilentRatherThanFailing pins the advisory contract: an unwired or
// failing store yields no refs and no panic, because every caller is a surface
// that must stay silent rather than break a session start.
func TestOpenIsSilentRatherThanFailing(t *testing.T) {
	if refs := Open(context.Background(), nil); refs != nil {
		t.Errorf("nil lister = %+v, want nil", refs)
	}
	failing := func(context.Context, workitem.ListFilter) ([]workitem.Item, error) {
		return nil, errors.New("store unavailable")
	}
	if refs := Open(context.Background(), failing); refs != nil {
		t.Errorf("failing lister = %+v, want nil", refs)
	}
}

// TestTagIsTheWireFormat guards the dedup key already carried by stories filed
// in live repos: change the string and every tracked document is re-filed.
func TestTagIsTheWireFormat(t *testing.T) {
	if got := Tag("workflows", "done"); got != "doc:workflows/done" {
		t.Errorf("Tag = %q, want doc:workflows/done — this is a wire format", got)
	}
	if MarkerTag != "type:system" || Priority != "high" {
		t.Errorf("marker/priority drifted from what reindex files: %q / %q", MarkerTag, Priority)
	}
}
