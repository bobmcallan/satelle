package workitem

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestMigrateAssigneeIdempotentOnPreColumnRows(t *testing.T) {
	db, err := sql.Open("sqlite", "file:workitem-pre-assignee-"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
CREATE TABLE work_items (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'backlog',
    priority TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    parent_id TEXT NOT NULL DEFAULT '',
    acceptance_criteria TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    archived INTEGER NOT NULL DEFAULT 0,
    park_origin TEXT NOT NULL DEFAULT ''
)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO work_items (id, kind, title, created_at, updated_at)
		VALUES ('sty_pre', 'story', 'pre', 't', 't')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	st := New(db)
	got, err := st.Get(context.Background(), "sty_pre")
	if err != nil {
		t.Fatal(err)
	}
	if got.Assignee != "" {
		t.Fatalf("pre-column row assignee = %q, want empty", got.Assignee)
	}
}

func openStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", "file:workitem-"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	return New(db)
}

func TestListChangedSincePagesPast2000(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	const n = 2100
	for i := 0; i < n; i++ {
		_, err := st.Create(ctx, CreateInput{
			Kind: KindStory, Title: fmt.Sprintf("s%d", i), Body: "b",
			AcceptanceCriteria: "1. a", Status: "backlog", Category: "chore",
		}, now.Add(time.Duration(i)*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
	}
	listed, err := st.List(ctx, ListFilter{Kind: KindStory, Limit: 5000, IncludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2000 {
		t.Fatalf("List returned %d, want 2000 cap", len(listed))
	}
	var all []Item
	offset := 0
	for {
		page, err := st.ListChangedSince(ctx, KindStory, time.Time{}, 500, offset)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		if len(page) < 500 {
			break
		}
		offset += len(page)
	}
	if len(all) != n {
		t.Fatalf("ListChangedSince paged %d, want %d", len(all), n)
	}
}
