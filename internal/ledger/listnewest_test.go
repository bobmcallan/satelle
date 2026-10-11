package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// seedStoryRows appends n rows for story, bodied "row-1".."row-n", one minute
// apart so created_at orders them as numbered.
func seedStoryRows(t *testing.T, s *Store, story string, n int) {
	t.Helper()
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= n; i++ {
		if _, err := s.Append(context.Background(), AppendInput{
			StoryID: story, Kind: "note", Body: fmt.Sprintf("row-%d", i),
		}, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
}

// TestListNewestWindow pins sty_523edea7 AC1/AC2 at the store: a ledger longer
// than the default limit lists its NEWEST rows, oldest-first with the newest
// last, and the Window states what was left out; List keeps the oldest-first
// window the internal readers rely on.
func TestListNewestWindow(t *testing.T) {
	db, err := sql.Open("sqlite", "file:listnewest?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	ctx := context.Background()
	seedStoryRows(t, s, "sty_long", 250)
	seedStoryRows(t, s, "sty_short", 10)

	got, win, err := s.ListNewest(ctx, ListFilter{StoryID: "sty_long"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != DefaultListLimit {
		t.Fatalf("len=%d want %d", len(got), DefaultListLimit)
	}
	if got[0].Body != "row-51" || got[len(got)-1].Body != "row-250" {
		t.Fatalf("window is %q..%q, want row-51..row-250 (newest, oldest-first)", got[0].Body, got[len(got)-1].Body)
	}
	for i := 1; i < len(got); i++ {
		if got[i].CreatedAt.Before(got[i-1].CreatedAt) {
			t.Fatalf("row %d is older than row %d: not oldest-first", i, i-1)
		}
	}
	if want := (Window{Total: 250, Limit: 200, Omitted: 50, Max: MaxListLimit}); win != want {
		t.Fatalf("window=%+v want %+v", win, want)
	}

	// A limit that fits everything omits nothing.
	got, win, err = s.ListNewest(ctx, ListFilter{StoryID: "sty_long", Limit: 300})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 250 || win.Omitted != 0 || win.Total != 250 || win.Limit != 300 {
		t.Fatalf("limit 300: len=%d window=%+v, want 250 rows and none omitted", len(got), win)
	}

	got, win, err = s.ListNewest(ctx, ListFilter{StoryID: "sty_short"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 || win.Omitted != 0 || win.Total != 10 {
		t.Fatalf("short ledger: len=%d window=%+v, want 10 rows and none omitted", len(got), win)
	}

	// No match is an empty window, not an error.
	got, win, err = s.ListNewest(ctx, ListFilter{StoryID: "sty_none"})
	if err != nil || len(got) != 0 || win.Total != 0 || win.Omitted != 0 {
		t.Fatalf("no match: got=%d window=%+v err=%v", len(got), win, err)
	}

	if _, _, err := s.ListNewest(ctx, ListFilter{}); err == nil {
		t.Fatal("an unfiltered ListNewest must be refused")
	}

	// List is unchanged: the OLDEST default window, oldest-first.
	old, err := s.List(ctx, ListFilter{StoryID: "sty_long"})
	if err != nil {
		t.Fatal(err)
	}
	if len(old) != DefaultListLimit || old[0].Body != "row-1" || old[len(old)-1].Body != "row-200" {
		t.Fatalf("List window changed: len=%d first=%q last=%q", len(old), old[0].Body, old[len(old)-1].Body)
	}
}

func TestEffectiveLimit(t *testing.T) {
	for in, want := range map[int]int{-1: DefaultListLimit, 0: DefaultListLimit, 7: 7, MaxListLimit: MaxListLimit, MaxListLimit + 1: MaxListLimit} {
		if got := EffectiveLimit(in); got != want {
			t.Errorf("EffectiveLimit(%d)=%d want %d", in, got, want)
		}
	}
}
