package ledger

import (
	"context"
	"slices"
	"testing"
	"time"
)

// sty_0dc600e1 AC8: a local append marks its story's item row pending; an event
// pulled through Upsert never does.
func TestAppendMarksTheStoryAndUpsertDoesNot(t *testing.T) {
	s := removeStore(t, t.Name())
	ctx := context.Background()
	if _, err := s.Append(ctx, AppendInput{StoryID: "sty_local", Kind: "note"}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendOnce(ctx, AppendInput{StoryID: "sty_once", Kind: "note", Refs: []byte(`{"k":"v"}`)}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(ctx, Entry{ID: "evt_pulled", StoryID: "sty_pulled", Kind: "note"}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ctx, AppendInput{Kind: "note"}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Marked(ctx, MarkItemPending)
	if err != nil {
		t.Fatal(err)
	}
	if !got["sty_local"] || !got["sty_once"] || got["sty_pulled"] || len(got) != 2 {
		t.Fatalf("pending = %v, want sty_local and sty_once only", got)
	}
	if err := s.ClearMarks(ctx, MarkItemPending, []string{"sty_local"}); err != nil {
		t.Fatal(err)
	}
	if on, _ := s.HasMark(ctx, "sty_local", MarkItemPending); on {
		t.Fatal("the cleared mark is still set")
	}
}

// DropStoryHistory removes a story's events only when the chain ends at the head
// the server accepted, and marks it remote.
func TestDropStoryHistoryKeepsUnpublishedEvents(t *testing.T) {
	s := removeStore(t, t.Name())
	ctx := context.Background()
	first, _ := s.Append(ctx, AppendInput{StoryID: "sty_a", Kind: "note"}, time.Time{})
	second, _ := s.Append(ctx, AppendInput{StoryID: "sty_a", Kind: "note"}, time.Time{})

	if dropped, err := s.DropStoryHistory(ctx, "sty_a", first.ID); err != nil || dropped {
		t.Fatalf("dropped a chain with an unpublished event: %v %v", dropped, err)
	}
	if n := len(chainIDs(t, s, "sty_a")); n != 2 {
		t.Fatalf("events = %d, want 2", n)
	}
	if dropped, err := s.DropStoryHistory(ctx, "sty_a", second.ID); err != nil || !dropped {
		t.Fatalf("a published chain was not dropped: %v %v", dropped, err)
	}
	if n := len(chainIDs(t, s, "sty_a")); n != 0 {
		t.Fatalf("events = %d after drop, want 0", n)
	}
	if on, _ := s.HasMark(ctx, "sty_a", MarkHistoryRemote); !on {
		t.Fatal("the dropped story is not marked remote")
	}
}

// RebaseEvents writes the server's events first and the unpublished ones after,
// the first of them now building on the server's head.
func TestRebaseEventsPutsUnpublishedAfterTheServer(t *testing.T) {
	s := removeStore(t, t.Name())
	ctx := context.Background()
	root, _ := s.Append(ctx, AppendInput{StoryID: "sty_r", Kind: "note"}, time.Time{})
	mine1, _ := s.Append(ctx, AppendInput{StoryID: "sty_r", Kind: "note"}, time.Time{})
	mine2, _ := s.Append(ctx, AppendInput{StoryID: "sty_r", Kind: "note"}, time.Time{})
	theirs := Entry{ID: "evt_theirs", StoryID: "sty_r", Kind: "note", BaseID: root.ID, CreatedAt: time.Now()}

	if err := s.RebaseEvents(ctx, "sty_r", []Entry{theirs}, []string{mine1.ID, mine2.ID}, theirs.ID); err != nil {
		t.Fatal(err)
	}
	events, err := s.StoryChain(ctx, "sty_r")
	if err != nil {
		t.Fatal(err)
	}
	var ids, bases []string
	for _, e := range events {
		ids = append(ids, e.ID)
		bases = append(bases, e.BaseID)
	}
	if want := []string{root.ID, theirs.ID, mine1.ID, mine2.ID}; !slices.Equal(ids, want) {
		t.Fatalf("chain = %v, want %v", ids, want)
	}
	if want := []string{"", root.ID, theirs.ID, mine1.ID}; !slices.Equal(bases, want) {
		t.Fatalf("bases = %v, want %v", bases, want)
	}
	if breaks := VerifyChain(events); len(breaks) != 0 {
		t.Fatalf("rebased chain breaks: %v", breaks)
	}
}
