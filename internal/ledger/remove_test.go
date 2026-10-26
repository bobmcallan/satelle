package ledger

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func removeStore(t *testing.T, name string) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	return New(db)
}

func chainIDs(t *testing.T, s *Store, story string) []string {
	t.Helper()
	events, err := s.StoryChain(context.Background(), story)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	return ids
}

// RemoveEvents drops exactly the events the guard accepts and leaves every other
// story and event alone; a guard that declines any one, an id the story does not
// hold, or a surviving event built on a removed one leaves the table unchanged.
func TestRemoveEvents(t *testing.T) {
	ctx := context.Background()
	s := removeStore(t, "removeevents")
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	add := func(story, kind string) Entry {
		t.Helper()
		e, err := s.Append(ctx, AppendInput{StoryID: story, Kind: kind, Actor: "satelle"}, now)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	seed1, seed2 := add("sty_a", "seed"), add("sty_a", "seed")
	other := add("sty_b", "seed")
	onlySeed := func(e Entry) bool { return e.Kind == "seed" }

	if err := s.RemoveEvents(ctx, "sty_a", []string{seed1.ID}, onlySeed); !errors.Is(err, ErrRemoveRefused) {
		t.Fatalf("removing an event another builds on = %v, want ErrRemoveRefused", err)
	}
	if err := s.RemoveEvents(ctx, "sty_a", []string{other.ID}, onlySeed); !errors.Is(err, ErrRemoveRefused) {
		t.Fatalf("removing another story's event = %v, want ErrRemoveRefused", err)
	}
	work := add("sty_a", "work")
	if err := s.RemoveEvents(ctx, "sty_a", []string{seed1.ID, seed2.ID, work.ID}, onlySeed); !errors.Is(err, ErrRemoveRefused) {
		t.Fatalf("a guard that declines one = %v, want ErrRemoveRefused", err)
	}
	if got, want := chainIDs(t, s, "sty_a"), []string{seed1.ID, seed2.ID, work.ID}; !slices.Equal(got, want) {
		t.Fatalf("a refused removal changed the chain: %v, want %v", got, want)
	}

	// Where nothing builds on them, the two seeds go together and no other story moves.
	s2 := removeStore(t, "removeevents2")
	a1, err := s2.Append(ctx, AppendInput{StoryID: "sty_a", Kind: "seed"}, now)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := s2.Append(ctx, AppendInput{StoryID: "sty_a", Kind: "seed"}, now)
	if err != nil {
		t.Fatal(err)
	}
	keep, err := s2.Append(ctx, AppendInput{StoryID: "sty_b", Kind: "seed"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.RemoveEvents(ctx, "sty_a", []string{a1.ID, a2.ID}, onlySeed); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := chainIDs(t, s2, "sty_a"); len(got) != 0 {
		t.Fatalf("sty_a still has %v", got)
	}
	if got := chainIDs(t, s2, "sty_b"); !slices.Equal(got, []string{keep.ID}) {
		t.Fatalf("sty_b = %v, want its one event kept", got)
	}
}

// IsVersionEvent reads the identity versionRefs gives an event, not its kind alone.
func TestIsVersionEvent(t *testing.T) {
	in := versionInput("sty_a", WorkflowSource{Hash: "h", Legacy: "x"})
	if !IsVersionEvent(Entry{Kind: in.Kind, Refs: in.Refs}) {
		t.Error("a version event was not recognised")
	}
	if IsVersionEvent(Entry{Kind: KindWorkflowVersion, Refs: []byte("[]")}) || IsVersionEvent(Entry{Kind: "comment", Refs: in.Refs}) {
		t.Error("an event that is not a version event was recognised")
	}
}
