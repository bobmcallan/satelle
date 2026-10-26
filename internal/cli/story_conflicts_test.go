package cli

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// AC2 (sty_6e5fc41c): a story row a pass keeps because the local copy is newer is
// a conflict when the content differs, and nothing when it does not; applying a
// story, or finding both copies identical, clears it. No case rewrites the local
// copy.
func TestUpsertPageRecordsStoryConflictsNotIdenticalRows(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	created := time.Date(2026, 8, 13, 2, 0, 0, 0, time.UTC)
	newer := created.Add(2 * time.Hour)
	optIn := map[string]bool{"stories": true}

	ahead, err := a.Store.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "T", Status: workitem.StatusDone}, newer)
	if err != nil {
		t.Fatal(err)
	}
	same, err := a.Store.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "T", Status: workitem.StatusBacklog}, newer)
	if err != nil {
		t.Fatal(err)
	}
	older, err := a.Store.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "T", Status: workitem.StatusBacklog}, created.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	page := []hosted.WorkstateItem{
		hostedStory(ahead.ID, workitem.StatusBacklog, created),
		hostedStory(same.ID, workitem.StatusBacklog, created),
		hostedStory(older.ID, workitem.StatusBacklog, created),
	}

	var out storyOutcome
	_, nItems, _, nKept, err := upsertWorkstatePage(ctx, a, optIn, page, nil, false, newer, &out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if nKept != 2 || nItems != 1 {
		t.Fatalf("kept=%d applied=%d, want 2 kept (ahead, same) and 1 applied (older)", nKept, nItems)
	}
	if len(out.Conflicts) != 1 || out.Conflicts[ahead.ID] != conflictLocalAhead {
		t.Fatalf("conflicts = %v, want only %s (%s)", out.Conflicts, ahead.ID, conflictLocalAhead)
	}
	for _, id := range []string{same.ID, older.ID} {
		if !slices.Contains(out.Cleared, id) {
			t.Errorf("%s (identical or applied) must clear a recorded conflict; cleared = %v", id, out.Cleared)
		}
	}
	if slices.Contains(out.Cleared, ahead.ID) {
		t.Errorf("the conflicting story must not be cleared: %v", out.Cleared)
	}
	got, err := a.Store.Stories.Get(ctx, ahead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != workitem.StatusDone {
		t.Errorf("recording the conflict changed the local status to %q", got.Status)
	}

	// A nil outcome is a caller with nowhere to record: it must not panic.
	if _, _, _, _, err := upsertWorkstatePage(ctx, a, optIn, page, nil, false, newer, nil, nil); err != nil {
		t.Fatal(err)
	}
}

// A refused pull names the stories that differ; the outcome records exactly them.
func TestPullRefusalRecordsTheDifferingStories(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	created := time.Date(2026, 8, 13, 2, 0, 0, 0, time.UTC)
	newer := created.Add(2 * time.Hour)
	ahead, err := a.Store.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "T", Status: workitem.StatusDone}, newer)
	if err != nil {
		t.Fatal(err)
	}
	same, err := a.Store.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "T", Status: workitem.StatusBacklog}, newer)
	if err != nil {
		t.Fatal(err)
	}
	cerr := checkWorkstatePullConflicts(ctx, a, map[string]bool{"stories": true}, []hosted.WorkstateItem{
		hostedStory(ahead.ID, workitem.StatusBacklog, created),
		hostedStory(same.ID, workitem.StatusBacklog, created),
	}, nil)
	if !errors.Is(cerr, ErrWorkstatePullConflict) {
		t.Fatalf("err = %v, want the work-state pull conflict", cerr)
	}
	var out storyOutcome
	out.refused(cerr)
	if len(out.Conflicts) != 1 || out.Conflicts[ahead.ID] != conflictPullRefused {
		t.Fatalf("conflicts = %v, want only %s (%s)", out.Conflicts, ahead.ID, conflictPullRefused)
	}
	out = storyOutcome{}
	out.refused(errors.New("network down"))
	if len(out.Conflicts) != 0 {
		t.Fatalf("a non-conflict error recorded %v", out.Conflicts)
	}
}
