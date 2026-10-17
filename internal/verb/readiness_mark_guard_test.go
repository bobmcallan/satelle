package verb_test

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/verb"
)

// sty_6e3ffa2f: a story is never ready when it is created, whoever creates it;
// only an accepted readiness round sets the route's readiness mark. The mark here
// is "triaged" (or "lane:ready" where a reported change must be able to name it),
// so the behaviour under test is the binary reading the route, not a compiled-in
// tag name.

func createRefusal(t *testing.T, req map[string]any) error {
	t.Helper()
	_, err := dispatchRaw(t, "story-create", req)
	return err
}

// AC1: a create carrying the route's mark is refused, naming the tag, and stores
// nothing — at the default status and at a later one.
func TestCreateRefusesTheReadinessMark(t *testing.T) {
	newReadyRig(t, "triaged", readyReviewers)
	for _, status := range []string{"", "plan"} {
		before := len(listAll(t))
		req := map[string]any{"title": "raised ready", "body": "B", "acceptance_criteria": "1. one", "category": "feature", "tags": []string{"triaged"}}
		if status != "" {
			req["status"] = status
		}
		err := createRefusal(t, req)
		if err == nil || !strings.Contains(err.Error(), `"triaged"`) || !strings.Contains(err.Error(), "accepted readiness round") {
			t.Fatalf("status %q: want a refusal naming the tag and saying only accepted readiness sets it, got %v", status, err)
		}
		if got := len(listAll(t)); got != before {
			t.Errorf("status %q: a refused create must store nothing: %d items, was %d", status, got, before)
		}
	}
	// The same create without the mark is fine.
	newStoryOf(t, "feature", "plain")
}

// AC2: a story-set whose final tag set newly carries the mark is refused and
// leaves the tags unchanged, in both the additive and the full-replace mode; a
// replacement that keeps a mark the story already holds is accepted.
func TestSetRefusesAnUnheldReadinessMark(t *testing.T) {
	newReadyRig(t, "triaged", readyReviewers)
	a := newStoryOf(t, "feature", "a", "keep")
	for name, req := range map[string]map[string]any{
		"add_tags": {"id": a.ID, "add_tags": []string{"triaged"}},
		"tags":     {"id": a.ID, "tags": []string{"keep", "triaged"}},
	} {
		before := tagsOf(t, a.ID)
		if _, err := dispatchRaw(t, "story-set", req); err == nil || !strings.Contains(err.Error(), `"triaged"`) {
			t.Fatalf("%s: want a refusal naming the mark, got %v", name, err)
		}
		if got := tagsOf(t, a.ID); strings.Join(got, ",") != strings.Join(before, ",") {
			t.Errorf("%s: tags = %v, want unchanged %v", name, got, before)
		}
	}

	readyCall(t, a.ID)
	if !carries(tagsOf(t, a.ID), "triaged") {
		t.Fatalf("an accepted round must mark the story: %v", tagsOf(t, a.ID))
	}
	call(t, "story-set", map[string]any{"id": a.ID, "tags": []string{"triaged", "other"}})
	if got := tagsOf(t, a.ID); !carries(got, "triaged") || !carries(got, "other") {
		t.Errorf("a replacement keeping the held mark must be accepted: %v", got)
	}
}

// AC3: a reported add-tags that would put the mark on a story whose readiness does
// not hold is a limit breach: the whole report is refused and neither story's tags
// change. A reported child carrying the mark is refused the same way, as a create.
func TestReportedChangesRefuseTheReadinessMark(t *testing.T) {
	for name, changes := range map[string]func(w changesWorld) []agentartifact.Change{
		"add-tags onto an unready story": func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{
				{Op: "add-tags", Story: w.a.ID, Tags: []string{"order:2"}},
				{Op: "add-tags", Story: w.epic.ID, Tags: []string{"lane:ready"}},
			}
		},
		"a created child carrying the mark": func(w changesWorld) []agentartifact.Change {
			return []agentartifact.Change{
				{Op: "add-tags", Story: w.a.ID, Tags: []string{"order:2"}},
				{Op: "create-child", Title: "raised ready", Category: "feature", Tags: []string{"lane:ready"}},
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newChangesWorldMark(t, "lane:ready")
			w.r.d.perform = performerReports(w.epic.ID, changes(w), "")
			epicBefore := tagsOf(t, w.epic.ID)
			res := readyCall(t, w.epic.ID)[0]
			if res.Outcome != verb.ReadyNotAccepted || !strings.Contains(res.Reason, "none applied") || !strings.Contains(res.Reason, `"lane:ready"`) {
				t.Fatalf("want the report refused naming the mark with nothing applied, got %+v", res)
			}
			if carries(tagsOf(t, w.a.ID), "order:2") {
				t.Error("the valid change in the refused report must not be applied")
			}
			if got := tagsOf(t, w.epic.ID); strings.Join(got, ",") != strings.Join(epicBefore, ",") {
				t.Errorf("epic tags = %v, want unchanged %v", got, epicBefore)
			}
			if _, ok := titled(listAll(t), "raised ready"); ok {
				t.Error("a child carrying the mark must not be created")
			}
		})
	}
}

// AC5: a route whose readiness step declares no mark refuses no tag.
func TestNoDeclaredMarkRefusesNoTag(t *testing.T) {
	newReadyRig(t, "", readyReviewers)
	created := newStoryOf(t, "feature", "a", "ready")
	if !carries(tagsOf(t, created.ID), "ready") {
		t.Fatalf("a create tagged ready must be kept under a route with no mark: %v", tagsOf(t, created.ID))
	}
	b := newStoryOf(t, "feature", "b")
	call(t, "story-set", map[string]any{"id": b.ID, "add_tags": []string{"ready"}})
	if !carries(tagsOf(t, b.ID), "ready") {
		t.Errorf("add_tags ready must be accepted under a route with no mark: %v", tagsOf(t, b.ID))
	}
	call(t, "story-set", map[string]any{"id": b.ID, "tags": []string{"ready", "x"}})
	if !carries(tagsOf(t, b.ID), "x") {
		t.Errorf("a full replace must be accepted under a route with no mark: %v", tagsOf(t, b.ID))
	}
}
