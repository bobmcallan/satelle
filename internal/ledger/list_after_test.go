package ledger

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// A story longer than one list page is read whole by walking AfterID, including
// rows that share a timestamp (a gate writes its verdict rows in one instant).
func TestListAfterIDPagesAWholeStoryInOrder(t *testing.T) {
	db, err := sql.Open("sqlite", "file:listafter?mode=memory&cache=shared")
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
	for i := 0; i < 25; i++ {
		// Five rows per instant.
		if _, err := s.Append(ctx, AppendInput{StoryID: "sty_long", Kind: "noise", Body: "row"}, base.Add(time.Duration(i/5)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Append(ctx, AppendInput{StoryID: "sty_other", Kind: "noise", Body: "other"}, base); err != nil {
		t.Fatal(err)
	}
	all, err := s.List(ctx, ListFilter{StoryID: "sty_long", Limit: 100})
	if err != nil || len(all) != 25 {
		t.Fatalf("whole story = %d rows, %v", len(all), err)
	}
	var got []string
	for after := ""; ; {
		page, err := s.List(ctx, ListFilter{StoryID: "sty_long", Limit: 7, AfterID: after})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page {
			got = append(got, e.ID)
		}
		if len(page) < 7 {
			break
		}
		after = page[len(page)-1].ID
	}
	if len(got) != len(all) {
		t.Fatalf("paged %d rows, whole list has %d", len(got), len(all))
	}
	for i := range got {
		if got[i] != all[i].ID {
			t.Fatalf("page order differs at %d: %s vs %s", i, got[i], all[i].ID)
		}
	}
}
