package verb_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func archiveStories(t *testing.T, reason string, ids ...string) verb.ArchiveResult {
	t.Helper()
	var res verb.ArchiveResult
	if err := json.Unmarshal(call(t, "story-archive", map[string]any{"ids": ids, "reason": reason}), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func listIDs(t *testing.T, req map[string]any) []string {
	t.Helper()
	var items []workitem.Item
	if err := json.Unmarshal(call(t, "story-list", req), &items); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

// archivedChange is the archived field change a story_updated row names, "" when
// it names none.
func archivedChange(t *testing.T, e ledger.Entry) (old, new, reason string) {
	t.Helper()
	var p struct {
		Fields []struct{ Field, Old, New string } `json:"fields"`
		Reason string                             `json:"reason"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatal(err)
	}
	for _, f := range p.Fields {
		if f.Field == "archived" {
			return f.Old, f.New, p.Reason
		}
	}
	return "", "", ""
}

func archiveRows(t *testing.T, id string) []ledger.Entry {
	t.Helper()
	var out []ledger.Entry
	for _, e := range ledgerRows(t, id, ledger.KindStoryUpdated) {
		if _, n, _ := archivedChange(t, e); n != "" {
			out = append(out, e)
		}
	}
	return out
}

func chainKinds(t *testing.T, id string) []string {
	t.Helper()
	var all []ledger.Entry
	if err := json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": id}), &all); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range all {
		out = append(out, e.ID+":"+e.Kind)
	}
	return out
}

// sty_28665836 AC1: archive appends one story_updated whose payload changes
// archived false→true with the reason and an actor; the story leaves the default
// list but story get still returns it.
func TestStoryArchiveRecordsTheFieldChangeAndHidesTheStory(t *testing.T) {
	wireWithWorkflows(t, freezeWF)
	a := createFolded(t, map[string]any{"title": "archive me", "category": "feature"})
	keep := createFolded(t, map[string]any{"title": "keep me", "category": "feature"})

	res := archiveStories(t, "not a priority", a.ID)
	if !slices.Equal(res.Changed, []string{a.ID}) {
		t.Fatalf("changed = %v, want [%s]", res.Changed, a.ID)
	}
	rows := archiveRows(t, a.ID)
	if len(rows) != 1 {
		t.Fatalf("archived story_updated rows = %d, want 1", len(rows))
	}
	old, nw, reason := archivedChange(t, rows[0])
	if old != "false" || nw != "true" || reason != "not a priority" {
		t.Errorf("event = %q→%q reason %q, want false→true with the reason", old, nw, reason)
	}
	if rows[0].Actor == "" {
		t.Error("the event names no actor")
	}

	if ids := listIDs(t, map[string]any{}); slices.Contains(ids, a.ID) || !slices.Contains(ids, keep.ID) {
		t.Errorf("default list = %v, want %s hidden and %s shown", ids, a.ID, keep.ID)
	}
	if got := getItem(t, a.ID); got.ID != a.ID || !got.Archived {
		t.Errorf("story get = %+v, want the archived story", got)
	}
}

// sty_28665836 AC1: an archived child leaves the epic's wave, and naming the epic
// archives its children at the start state, listing the others as skipped.
func TestStoryArchiveOfAnEpicTakesItsStartStateChildren(t *testing.T) {
	db := wireWithWorkflowsStore(t, freezeWF)
	epic := createFolded(t, map[string]any{"title": "container", "category": "epic-parent", "tags": []string{"epic:arch"}})
	a := createFolded(t, map[string]any{"title": "a", "category": "feature", "tags": []string{"epic:arch"}})
	b := createFolded(t, map[string]any{"title": "b", "category": "feature", "tags": []string{"epic:arch"}})
	// b is being worked: its chain says in_progress.
	if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: b.ID, Kind: ledger.KindStatusTransition, Actor: "executor",
		Payload: json.RawMessage(`{"from":"backlog","to":"in_progress"}`),
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	wave := func(id string) []string {
		t.Helper()
		var w verb.WaveResult
		if err := json.Unmarshal(call(t, "story-wave", map[string]any{"id": id}), &w); err != nil {
			t.Fatal(err)
		}
		return w.Runnable
	}
	if run := wave(epic.ID); !slices.Contains(run, a.ID) {
		t.Fatalf("baseline wave = %v, want a runnable", run)
	}
	archiveStories(t, "", a.ID)
	if run := wave(epic.ID); slices.Contains(run, a.ID) {
		t.Errorf("wave = %v still carries the archived child", run)
	}
	call(t, "story-unarchive", map[string]any{"ids": []string{a.ID}})

	res := archiveStories(t, "epic dropped", epic.ID)
	if !slices.Contains(res.Changed, epic.ID) || !slices.Contains(res.Changed, a.ID) || slices.Contains(res.Changed, b.ID) {
		t.Errorf("changed = %v, want the epic and a, not the performing b", res.Changed)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].ID != b.ID || res.Skipped[0].Status != "in_progress" {
		t.Errorf("skipped = %+v, want b at in_progress", res.Skipped)
	}
}

// sty_28665836 AC2: a story not at the start state is refused by id and status,
// a batch with one refusal archives nothing, and a successful archive moves no
// status and adds no event but its one story_updated.
func TestStoryArchiveRefusesAStoryPastTheStartStateAndMovesNoStatus(t *testing.T) {
	wireWithWorkflows(t, freezeWF)
	a := createFolded(t, map[string]any{"title": "a", "category": "feature"})
	working := createFolded(t, map[string]any{"title": "working", "category": "feature", "status": "in_progress"})
	finished := createFolded(t, map[string]any{"title": "finished", "category": "feature", "status": "done"})

	for _, c := range []struct{ id, status string }{{working.ID, "in_progress"}, {finished.ID, "done"}} {
		err := dispatchErr(t, "story-archive", map[string]any{"ids": []string{c.id}})
		if !strings.Contains(err.Error(), c.id) || !strings.Contains(err.Error(), c.status) {
			t.Errorf("refusal = %v, want it to name %s and %s", err, c.id, c.status)
		}
	}

	before := chainKinds(t, a.ID)
	dispatchErr(t, "story-archive", map[string]any{"ids": []string{a.ID, working.ID}})
	if got := getItem(t, a.ID); got.Archived {
		t.Error("a mixed batch archived the story that could be")
	}
	if after := chainKinds(t, a.ID); !slices.Equal(before, after) {
		t.Errorf("the refused batch changed a's chain: %v → %v", before, after)
	}

	transitions := func() []string {
		var out []string
		for _, e := range ledgerRows(t, a.ID, ledger.KindStatusTransition) {
			out = append(out, e.ID)
		}
		return out
	}
	statusBefore, transBefore := getItem(t, a.ID).Status, transitions()
	archiveStories(t, "", a.ID)
	if got := getItem(t, a.ID); got.Status != statusBefore {
		t.Errorf("status %q → %q, want it unchanged", statusBefore, got.Status)
	}
	if !slices.Equal(transBefore, transitions()) {
		t.Error("archive changed the story's status_transition events")
	}
	after := chainKinds(t, a.ID)
	if len(after) != len(before)+1 || !strings.HasSuffix(after[len(after)-1], ":"+ledger.KindStoryUpdated) {
		t.Errorf("chain %v → %v, want exactly one added story_updated", before, after)
	}
}

// sty_28665836 AC3: unarchive appends the true→false change with the actor and
// the story returns to the default list.
func TestStoryUnarchiveRestoresTheStoryToTheDefaultList(t *testing.T) {
	wireWithWorkflows(t, freezeWF)
	a := createFolded(t, map[string]any{"title": "a", "category": "feature"})
	archiveStories(t, "", a.ID)
	call(t, "story-unarchive", map[string]any{"ids": []string{a.ID}, "reason": "changed my mind"})

	rows := archiveRows(t, a.ID)
	if len(rows) != 2 {
		t.Fatalf("archived story_updated rows = %d, want 2", len(rows))
	}
	old, nw, reason := archivedChange(t, rows[1])
	if old != "true" || nw != "false" || reason != "changed my mind" || rows[1].Actor == "" {
		t.Errorf("unarchive event = %q→%q reason %q actor %q", old, nw, reason, rows[1].Actor)
	}
	if !slices.Contains(listIDs(t, map[string]any{}), a.ID) {
		t.Error("the story is not back in the default list")
	}
}

// sty_28665836 AC4: story list --archived lists only archived stories.
func TestStoryListArchivedListsOnlyArchivedStories(t *testing.T) {
	wireWithWorkflows(t, freezeWF)
	a := createFolded(t, map[string]any{"title": "a", "category": "feature"})
	createFolded(t, map[string]any{"title": "b", "category": "feature"})
	archiveStories(t, "", a.ID)
	if ids := listIDs(t, map[string]any{"archived": true}); !slices.Equal(ids, []string{a.ID}) {
		t.Errorf("list --archived = %v, want only %s", ids, a.ID)
	}
}

// sty_28665836 AC5: the archived flag is the chain's — a row whose value was
// cleared is rebuilt from it.
func TestRebuildRowRestoresArchivedFromTheChain(t *testing.T) {
	db := wireWithWorkflowsStore(t, freezeWF)
	ctx := context.Background()
	a := createFolded(t, map[string]any{"title": "a", "category": "feature"})
	archiveStories(t, "", a.ID)

	if _, err := db.SQL().Exec(`UPDATE work_items SET archived = 0 WHERE id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}
	row, err := db.Stories.Get(ctx, a.ID)
	if err != nil || row.Archived {
		t.Fatalf("setup: row %+v err %v", row, err)
	}
	got, err := verb.RebuildRow(ctx, db.Stories, db.Ledger, row, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Archived {
		t.Error("RebuildRow left the row unarchived although its chain archived it")
	}
	if stored, _ := db.Stories.Get(ctx, a.ID); !stored.Archived {
		t.Error("the rebuilt row was not written")
	}

	// And the reverse: a story its chain unarchived is not left archived.
	call(t, "story-unarchive", map[string]any{"ids": []string{a.ID}})
	if _, err := db.SQL().Exec(`UPDATE work_items SET archived = 1 WHERE id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}
	row, _ = db.Stories.Get(ctx, a.ID)
	if got, err := verb.RebuildRow(ctx, db.Stories, db.Ledger, row, time.Now()); err != nil || got.Archived {
		t.Errorf("RebuildRow after unarchive: archived %v err %v, want false", got.Archived, err)
	}
}
