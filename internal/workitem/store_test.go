package workitem

import (
	"context"
	"database/sql"
	"errors"
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

func TestUpsertPreservesParkOriginWhenIncomingBlank(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	it, err := st.Create(ctx, CreateInput{
		Kind: KindStory, Title: "parked", Body: "b", AcceptanceCriteria: "1. a",
		Status: "blocked",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	origin := "in_progress"
	if _, err := st.Update(ctx, it.ID, UpdateInput{ParkOrigin: &origin}, now); err != nil {
		t.Fatal(err)
	}
	incoming := it
	incoming.ParkOrigin = ""
	incoming.Status = "blocked"
	if _, err := st.Upsert(ctx, incoming, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ParkOrigin != "in_progress" {
		t.Errorf("Upsert wiped park_origin to %q", got.ParkOrigin)
	}
}

// sty_2c71eff6: a writer that read the row, spent minutes in a gate and a
// dispatched agent run, and then wrote back must lose to whoever moved the
// status meanwhile — the CAS says so, and names both statuses.
func TestUpdateCompareAndSetRefusesStaleStatus(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	it, err := st.Create(ctx, CreateInput{
		Kind: KindStory, Title: "cas", Body: "b", AcceptanceCriteria: "1. a", Status: "backlog",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetStatus(ctx, it.ID, "in_progress", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	stale := "backlog"
	body := "late planner write"
	_, err = st.Update(ctx, it.ID, UpdateInput{Body: &body, Status: &stale, ExpectStatus: &stale}, now.Add(2*time.Second))
	var conflict *StatusConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Update err = %v, want *StatusConflictError", err)
	}
	if conflict.Expected != "backlog" || conflict.Actual != "in_progress" {
		t.Errorf("conflict = %+v, want expected backlog / actual in_progress", conflict)
	}
	if !errors.Is(err, ErrStatusConflict) {
		t.Errorf("errors.Is(err, ErrStatusConflict) = false")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("a CAS conflict must not surface as ErrNotFound")
	}
	got, err := st.Get(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" || got.Body != "b" {
		t.Errorf("refused write leaked: status=%q body=%q", got.Status, got.Body)
	}
	// The matching snapshot still writes: CAS refuses staleness, not concurrency.
	fresh := "in_progress"
	if _, err := st.Update(ctx, it.ID, UpdateInput{Body: &body, ExpectStatus: &fresh}, now.Add(3*time.Second)); err != nil {
		t.Fatalf("current-snapshot write refused: %v", err)
	}
	// An unknown id is still ErrNotFound, not a conflict.
	if _, err := st.Update(ctx, "sty_missing", UpdateInput{Body: &body, ExpectStatus: &fresh}, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id err = %v, want ErrNotFound", err)
	}
}

// sty_2c71eff6: Upsert is a full-row write, so a stale import snapshot would
// otherwise rewrite status backwards. The older stamp keeps the stored status;
// UpsertForce is the explicit opt-out the --force workstate pull uses.
func TestUpsertKeepsStoredStatusForOlderSnapshot(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	it, err := st.Create(ctx, CreateInput{
		Kind: KindStory, Title: "stale-import", Body: "b", AcceptanceCriteria: "1. a", Status: "backlog",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := st.SetStatus(ctx, it.ID, "in_progress", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	stale := it
	stale.Status = "backlog"
	stale.Body = "snapshot body"
	stale.UpdatedAt = moved.UpdatedAt.Add(-time.Second)
	if _, err := st.Upsert(ctx, stale, now); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" {
		t.Errorf("stale Upsert regressed status to %q", got.Status)
	}
	if got.Body != "snapshot body" {
		t.Errorf("guard also blocked the non-status fields: body = %q", got.Body)
	}
	if _, err := st.UpsertForce(ctx, stale, now); err != nil {
		t.Fatal(err)
	}
	forced, err := st.Get(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if forced.Status != "backlog" {
		t.Errorf("UpsertForce status = %q, want the incoming backlog", forced.Status)
	}
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
