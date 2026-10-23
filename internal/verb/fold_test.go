package verb_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/storyfold"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// tagChangeRows returns id's story_updated rows that name a tag change, oldest
// first, with the change each names.
func tagChangeRows(t *testing.T, id string) (rows []ledger.Entry, changes []storyfold.TagsChange, sources []string) {
	t.Helper()
	for _, e := range ledgerRows(t, id, ledger.KindStoryUpdated) {
		var p struct {
			Tags   *storyfold.TagsChange `json:"tags"`
			Source string                `json:"source"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("story_updated payload: %v", err)
		}
		if p.Tags != nil {
			rows, changes, sources = append(rows, e), append(changes, *p.Tags), append(sources, p.Source)
		}
	}
	return rows, changes, sources
}

func getItem(t *testing.T, id string) workitem.Item {
	t.Helper()
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-get", map[string]any{"id": id}), &it); err != nil {
		t.Fatal(err)
	}
	return it
}

// sty_d84be8bc AC1: a story's creation carries its title, body, acceptance
// criteria, category, tags and initial status, so the fold starts from it.
func TestStoryCreatedCarriesTheWholeDefinitionAndInitialStatus(t *testing.T) {
	wire(t)
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "defined", "body": "the body", "acceptance_criteria": "1. a", "category": "feature",
		"tags": []string{"surface:cli", "order:1"},
	}), &it); err != nil {
		t.Fatal(err)
	}
	rows := ledgerRows(t, it.ID, ledger.KindStoryCreated)
	if len(rows) != 1 {
		t.Fatalf("story_created rows = %d, want 1", len(rows))
	}
	var p storyfold.CreatedPayload
	if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Title != "defined" || p.Body != "the body" || p.AcceptanceCriteria != "1. a" || p.Category != "feature" ||
		p.Status != "backlog" || !slices.Equal(p.Tags, it.Tags) || len(p.Tags) < 2 {
		t.Errorf("story_created payload = %+v, want the definition, the stamped tags %v and status backlog", p, it.Tags)
	}
}

// sty_d84be8bc AC1: every writer of a story's tags appends a story_updated naming
// the tags before and after — story set, estimate, actual and restamp here; the
// performer's reported tags, the migrate restamp and the reindex story in their
// own tests.
func TestEveryTagWriterNamesTheTagsBeforeAndAfter(t *testing.T) {
	wire(t)
	wireResolver(t, &fakeWorkflowResolver{
		byCategory: map[string]string{"feature": "wf-a"},
		states:     map[string][]string{"wf-a": {"backlog", "in_progress", "done"}, "wf-b": {"backlog", "in_progress", "done"}},
	})
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "tagged", "category": "feature", "tags": []string{"keep"}}), &it); err != nil {
		t.Fatal(err)
	}

	writers := []struct {
		name   string
		source string // the source a standalone tag event names; "" for the story-set event
		run    func()
	}{
		{"story set", "", func() { call(t, "story-set", map[string]any{"id": it.ID, "add_tags": []string{"order:1"}}) }},
		{"story estimate", "story-estimate", func() {
			call(t, "story-estimate", map[string]any{"id": it.ID, "fresh_input": 900, "output": 100})
		}},
		{"story actual", "story-actual", func() { call(t, "story-actual", map[string]any{"id": it.ID}) }},
		{"story restamp", "story-restamp", func() { call(t, "story-restamp", map[string]any{"id": it.ID, "workflow": "wf-b"}) }},
	}
	for _, w := range writers {
		t.Run(w.name, func(t *testing.T) {
			before := getItem(t, it.ID).Tags
			rowsBefore, _, _ := tagChangeRows(t, it.ID)
			w.run()
			after := getItem(t, it.ID).Tags
			rows, changes, sources := tagChangeRows(t, it.ID)
			if len(rows) != len(rowsBefore)+1 {
				t.Fatalf("%s wrote %d tag-change rows, want exactly 1", w.name, len(rows)-len(rowsBefore))
			}
			got := changes[len(changes)-1]
			if !slices.Equal(got.Before, before) || !slices.Equal(got.After, after) || slices.Equal(got.Before, got.After) {
				t.Errorf("%s: payload tags = %+v, want before %v and after %v", w.name, got, before, after)
			}
			if w.source != "" && sources[len(sources)-1] != w.source {
				t.Errorf("%s: source = %q, want %q", w.name, sources[len(sources)-1], w.source)
			}
		})
	}
}

// sty_d84be8bc AC1: the tags a readiness performer reports are recorded in the
// same shape — the tags before and after, with the performer's source step.
func TestPerformerTagChangeNamesTheTagsBeforeAndAfter(t *testing.T) {
	w := newChangesWorld(t)
	before := tagsOf(t, w.a.ID)
	w.r.d.perform = performerReports(w.epic.ID, []agentartifact.Change{
		{Op: "add-tags", Story: w.a.ID, Tags: []string{"order:2", "surface:cli"}},
	}, "")
	if res := readyCall(t, w.epic.ID)[0]; res.Outcome != verb.ReadyMarked {
		t.Fatalf("the epic must be readied: %+v", res)
	}
	after := tagsOf(t, w.a.ID)

	// The round's own readiness mark is a later tag change by story set; the
	// performer's is the one that names its source step.
	_, changes, sources := tagChangeRows(t, w.a.ID)
	i := slices.Index(sources, "performer:ready")
	if i < 0 {
		t.Fatalf("no tag change from performer:ready among %v", sources)
	}
	if got := changes[i]; !slices.Contains(got.After, "order:2") || !slices.Contains(got.After, "surface:cli") ||
		len(got.After) != len(got.Before)+2 {
		t.Errorf("payload tags = %+v, want the two reported tags added", got)
	}
	// Each change begins where the one before it ended, from the tags the story was
	// created with to the tags it ends with: the chain carries every tag write.
	prev := before
	for _, c := range changes {
		if !slices.Equal(c.Before, prev) {
			t.Errorf("a tag change begins at %v, but the one before it ended at %v", c.Before, prev)
		}
		prev = c.After
	}
	if !slices.Equal(prev, after) {
		t.Errorf("the tag changes end at %v, but the story carries %v", prev, after)
	}
	// story get answers from the fold, story list from the row: they agree.
	var listed []workitem.Item
	if err := json.Unmarshal(call(t, "story-list", map[string]any{}), &listed); err != nil {
		t.Fatal(err)
	}
	row := slices.IndexFunc(listed, func(it workitem.Item) bool { return it.ID == w.a.ID })
	if row < 0 || !slices.Equal(listed[row].Tags, getItem(t, w.a.ID).Tags) {
		t.Errorf("the row's tags differ from the fold's %v: %+v", getItem(t, w.a.ID).Tags, listed)
	}
}

// A tag change beside a transition is a story_updated of its own, as the
// transition event carries the status.
func TestATagChangeBesideATransitionIsItsOwnEvent(t *testing.T) {
	wire(t)
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "moved and tagged"}), &it); err != nil {
		t.Fatal(err)
	}
	before := getItem(t, it.ID).Tags
	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress", "add_tags": []string{"order:3"}})
	_, changes, sources := tagChangeRows(t, it.ID)
	if len(changes) != 1 || !slices.Equal(changes[0].Before, before) || !slices.Contains(changes[0].After, "order:3") || sources[0] != "story-set" {
		t.Errorf("tag changes = %+v (%v), want one from story-set adding order:3", changes, sources)
	}
	if rows := ledgerRows(t, it.ID, ledger.KindStatusTransition); len(rows) != 1 {
		t.Errorf("transitions = %d, want 1", len(rows))
	}
}

// sty_d84be8bc AC2: the fold of a story's chain equals the row the existing verbs
// wrote, through create, definition edits, tags, transitions, park and resume.
func TestFoldEqualsTheRowTheVerbsWrote(t *testing.T) {
	db := wire(t)
	ctx := context.Background()
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "folded", "body": "b", "acceptance_criteria": "1. x", "category": "feature", "tags": []string{"surface:cli"},
	}), &it); err != nil {
		t.Fatal(err)
	}
	check := func(step string) {
		t.Helper()
		row, err := db.Stories.Get(ctx, it.ID)
		if err != nil {
			t.Fatal(err)
		}
		st, breaks, err := verb.FoldStory(ctx, db.Ledger, row)
		if err != nil || len(breaks) != 0 {
			t.Fatalf("%s: fold: %v %+v", step, err, breaks)
		}
		for _, f := range storyfold.Fields {
			if !st.Defined[f] {
				t.Errorf("%s: the chain defines no %s", step, f)
			}
		}
		if d := storyfold.Compare(row, st); len(d) != 0 {
			t.Errorf("%s: the fold differs from the row the verbs wrote: %+v", step, d)
		}
	}
	check("create")
	call(t, "story-set", map[string]any{"id": it.ID, "title": "folded again", "body": "b2", "acceptance_criteria": "1. y"})
	check("definition edit")
	call(t, "story-set", map[string]any{"id": it.ID, "add_tags": []string{"order:1"}, "remove_tags": []string{"surface:cli"}})
	check("tag change")
	call(t, "story-estimate", map[string]any{"id": it.ID, "fresh_input": 10, "output": 5})
	check("estimate")
	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	check("transition")
	call(t, "story-set", map[string]any{"id": it.ID, "status": "blocked"})
	check("park")
	if row, _ := db.Stories.Get(ctx, it.ID); row.ParkOrigin != "in_progress" {
		t.Errorf("park origin = %q, want in_progress", row.ParkOrigin)
	}
	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	check("resume")
	if row, _ := db.Stories.Get(ctx, it.ID); row.ParkOrigin != "" || row.Status != "in_progress" {
		t.Errorf("after resume: status %q park origin %q", row.Status, row.ParkOrigin)
	}
}
