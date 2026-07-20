package ledger

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestListAllNewestFirstUnderCap(t *testing.T) {
	db, err := sql.Open("sqlite", "file:listall?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	ctx := context.Background()
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// Seed 15 old rows for other stories, then 1 recent for story S.
	for i := 0; i < 15; i++ {
		_, err := s.Append(ctx, AppendInput{StoryID: "sty_old", Kind: "noise", Body: "old"}, base.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
	}
	recent := base.Add(24 * time.Hour)
	if _, err := s.Append(ctx, AppendInput{StoryID: "sty_recent", Kind: "status_transition", Body: "recent"}, recent); err != nil {
		t.Fatal(err)
	}
	// Cap at 10: oldest-first would drop the recent row; newest-first keeps it.
	got, err := s.ListAll(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Fatalf("len=%d want 10", len(got))
	}
	if got[0].StoryID != "sty_recent" {
		t.Fatalf("first entry story=%q want sty_recent (newest-first)", got[0].StoryID)
	}
	var sawRecent bool
	for _, e := range got {
		if e.StoryID == "sty_recent" {
			sawRecent = true
		}
	}
	if !sawRecent {
		t.Fatal("recent story missing under cap — oldest-first regression")
	}
}
