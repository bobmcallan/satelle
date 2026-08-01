package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// routeWorkflow is a three-step lifecycle with a tag-scoped gate, so the route
// can be checked for both the always-on and the by-tag case.
var routeWorkflow = routeHalves(
	"## feature\n- raised\n- coded\n- closed\ncancel: cancelled\n",
	"## backlog\nstart: true\nprovides: raised\n\n"+
		"## in_progress\nagent: executor\nskills: code\nreviewers: intent-review\nreviewer_agent: reviewer\n"+
		"provides: coded\nrequires: raised\n\n"+
		"## done\nreviewers: done-review\nreviewer_agent: reviewer\nterminal: true\n"+
		"provides: closed\nrequires: coded\n\n"+
		"## gate design-review\nagent: reviewer\non: done\napplies_to: surface:ui\n")

// wireRoute opens a store with a story dir and the route fixture workflow
// indexed, so a story created here is governed by a parseable lifecycle.
func wireRoute(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetLeaseStore(db.Leases)
	verb.SetStoryDir(filepath.Join(dir, "stories"))
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocIndexStore(nil)
		verb.SetLeaseStore(nil)
		verb.SetStoryDir("")
	})

	wfDir := t.TempDir()
	for name, half := range routeWorkflow {
		if err := os.WriteFile(filepath.Join(wfDir, name+".md"), []byte(half), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	call(t, "doc-sync", map[string]any{"dirs": map[string]string{"workflows": wfDir}})
}

// TestRouteDocIsOneArtifactWrittenForward (sty_39e2d9df AC2 + AC5): the route and
// the reasoning behind every outcome are ONE document. The plan half is
// re-rendered so "you are here" stays true; the outcome half is APPENDED, so a
// second transition never erases the first one's verdicts.
func TestRouteDocIsOneArtifactWrittenForward(t *testing.T) {
	wireRoute(t)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{
		Gated: true, Accept: true, Skill: "intent-review",
		Reviewers: []verb.ReviewerVerdict{{
			Skill: "intent-review", Accept: true,
			Notes: "the story names a deliverable", Reasoning: "ACs are testable", Model: "opus",
		}},
	}})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "route me", "category": "feature", "body": "b", "acceptance_criteria": "1. a",
	}), &it)

	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})

	// Second edge, judged by a different reviewer — the appended block must not
	// displace the first.
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{
		Gated: true, Accept: true, Skill: "done-review",
		Reviewers: []verb.ReviewerVerdict{{
			Skill: "done-review", Accept: true, Notes: "every AC has evidence",
		}},
	}})
	call(t, "story-set", map[string]any{"id": it.ID, "status": "done"})

	body, err := verb.StoryRoute(context.Background(), it.ID)
	if err != nil {
		t.Fatalf("StoryRoute: %v", err)
	}

	// AC5: exactly one route artifact, and it is the one that carries the reasoning.
	var docs []struct{ StoryID, Name, Type string }
	json.Unmarshal(call(t, "story-doc-list", map[string]any{"story_id": it.ID}), &docs)
	routes := 0
	for _, d := range docs {
		if d.Type == verb.RouteDocName {
			routes++
		}
	}
	if routes != 1 {
		t.Errorf("story carries %d route documents; route and reasoning must be ONE artifact", routes)
	}

	// AC1/AC4: the plan half names every step and its gates, with no workflow file.
	for _, want := range []string{"## Route", "**backlog**", "**in_progress**", "@skill:code", "intent-review", "done-review"} {
		if !strings.Contains(body, want) {
			t.Errorf("route document is missing %q:\n%s", want, body)
		}
	}
	// AC2: both outcomes, both reviewers' reasoning, and a pointer to the full output.
	for _, want := range []string{
		"### backlog → in_progress",
		"### in_progress → done",
		"the story names a deliverable",
		"ACs are testable",
		"every AC has evidence",
		"satelle ledger list --story " + it.ID + " --kind review_accept",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("route document is missing %q:\n%s", want, body)
		}
	}
	if strings.Count(body, "### backlog → in_progress") != 1 {
		t.Error("an outcome must be appended once, not duplicated on each write")
	}
	if !strings.Contains(body, "* 3. **done**") {
		t.Errorf("the route must mark the story's CURRENT step after the transition:\n%s", body)
	}
}

// TestRouteRendersLiveBeforeAnyTransition (AC4): a story that has never moved
// still answers "what is my route", rendered from the governing workflow.
func TestRouteRendersLiveBeforeAnyTransition(t *testing.T) {
	wireRoute(t)

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "unmoved", "category": "feature", "body": "b", "acceptance_criteria": "1. a",
	}), &it)

	body, err := verb.StoryRoute(context.Background(), it.ID)
	if err != nil {
		t.Fatalf("StoryRoute: %v", err)
	}
	if !strings.Contains(body, "* 1. **backlog**") {
		t.Errorf("a backlog story's route must mark backlog as where it is:\n%s", body)
	}
	if !strings.Contains(body, "(no step has resolved yet)") {
		t.Errorf("an unmoved story's route must say no step has resolved:\n%s", body)
	}
}

// TestRouteRecordsAnUngatedAdvance: an edge whose declared gate does not resolve
// advanced with NOBODY judging it. The route must say so — a blank outcome would
// read as "reviewed and fine".
func TestRouteRecordsAnUngatedAdvance(t *testing.T) {
	wireRoute(t)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Unresolved: []string{"intent-review"}}})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "ungated", "category": "feature", "body": "b", "acceptance_criteria": "1. a",
	}), &it)
	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})

	body, err := verb.StoryRoute(context.Background(), it.ID)
	if err != nil {
		t.Fatalf("StoryRoute: %v", err)
	}
	if !strings.Contains(body, "NOT JUDGED") {
		t.Errorf("an unresolved gate must be recorded as an unjudged advance:\n%s", body)
	}
}
