package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// openTemp opens a fresh store in a temp dir, registering cleanup.
func openTemp(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".satelle", "satelle.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCreatesDBAndIsReopenable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".satelle", "satelle.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("db file not created: %v", err)
	}
	// Reopening an existing store re-runs migrations idempotently.
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	db2.Close()
}

func TestMigrateRenamesOpenToBacklog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".satelle", "satelle.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Simulate a row persisted under the former 'open' initial status.
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.SQL().Exec(
		`INSERT INTO work_items (id, kind, title, status, tags, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
		"sty_legacy", "story", "legacy", "open", "[]", ts, ts); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	db.Close()

	// Reopening re-runs Migrate, which renames any 'open' row to 'backlog'.
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	got, err := db2.Stories.Get(context.Background(), "sty_legacy")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != workitem.StatusBacklog {
		t.Errorf("legacy 'open' row not migrated: status = %q, want backlog", got.Status)
	}
}

func TestStoryLifecycle(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	now := time.Now()

	it, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind:  workitem.KindStory,
		Title: "Scaffold satelle",
		Tags:  []string{"mvp"},
	}, now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if it.ID == "" || it.ID[:4] != "sty_" {
		t.Errorf("story id = %q, want sty_ prefix", it.ID)
	}
	if it.Status != workitem.StatusBacklog {
		t.Errorf("default status = %q, want backlog", it.Status)
	}

	got, err := db.Stories.Get(ctx, it.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "Scaffold satelle" || len(got.Tags) != 1 || got.Tags[0] != "mvp" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	upd, err := db.Stories.SetStatus(ctx, it.ID, workitem.StatusDone, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("set status: %v", err)
	}
	if upd.Status != workitem.StatusDone {
		t.Errorf("status = %q, want done", upd.Status)
	}
	if !upd.UpdatedAt.After(upd.CreatedAt) {
		t.Errorf("updated_at not advanced: created=%v updated=%v", upd.CreatedAt, upd.UpdatedAt)
	}

	if _, err := db.Stories.Get(ctx, "sty_missing"); err != workitem.ErrNotFound {
		t.Errorf("missing get err = %v, want ErrNotFound", err)
	}
}

func TestInTxRollbackLeavesRowUnchanged(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	now := time.Now()
	it, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "tx"}, now)
	if err != nil {
		t.Fatal(err)
	}
	err = db.InTx(ctx, func(tx *sql.Tx) error {
		if _, uerr := db.Stories.WithTx(tx).SetStatus(ctx, it.ID, workitem.StatusDone, now.Add(time.Minute)); uerr != nil {
			return uerr
		}
		return errors.New("forced")
	})
	if err == nil || err.Error() != "forced" {
		t.Fatalf("want forced error, got %v", err)
	}
	got, gerr := db.Stories.Get(ctx, it.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if got.Status != workitem.StatusBacklog {
		t.Errorf("rolled-back row status = %q, want backlog", got.Status)
	}
}

func TestKindPartitioning(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	now := time.Now()

	if _, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "s1"}, now); err != nil {
		t.Fatal(err)
	}
	tsk, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindTask, Title: "t1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if tsk.ID[:4] != "tsk_" {
		t.Errorf("task id = %q, want tsk_ prefix", tsk.ID)
	}

	stories, err := db.Stories.List(ctx, workitem.ListFilter{Kind: workitem.KindStory})
	if err != nil {
		t.Fatal(err)
	}
	if len(stories) != 1 || stories[0].Title != "s1" {
		t.Errorf("story list = %+v, want only s1", stories)
	}
	all, _ := db.Stories.List(ctx, workitem.ListFilter{})
	if len(all) != 2 {
		t.Errorf("unfiltered list len = %d, want 2", len(all))
	}
}

// Tag filter (sty_f7115cd2): exact match against multi-value tags (repeated keys);
// composes with status/parent; ANY-match within a namespace.
func TestListFilterByTag(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	now := time.Now()

	multi, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "multi-epic", Status: "backlog",
		ParentID: "sty_parent01",
		Tags:     []string{"epic:this", "epic:that", "sprint:5", "area:cli"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "other-sprint", Status: "backlog",
		Tags: []string{"sprint:4", "area:cli"},
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "done-sprint5", Status: "done",
		Tags: []string{"sprint:5"},
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindTask, Title: "task-sprint5",
		Tags: []string{"sprint:5"},
	}, now); err != nil {
		t.Fatal(err)
	}

	// ANY-match: multi holds both epic:this and epic:that → matches epic:this
	got, err := db.Stories.List(ctx, workitem.ListFilter{Tag: "epic:this"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != multi.ID {
		t.Errorf("tag epic:this = %+v, want only multi", got)
	}

	// Classification axis still filters
	got, err = db.Stories.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Tag: "sprint:5"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("story sprint:5 len = %d, want 2", len(got))
	}

	// Composes with status (+ kind so default-status tasks don't match)
	got, err = db.Stories.List(ctx, workitem.ListFilter{
		Kind: workitem.KindStory, Tag: "sprint:5", Status: "backlog",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != multi.ID {
		t.Errorf("sprint:5+backlog = %+v, want multi only", got)
	}

	// Composes with parent
	got, err = db.Stories.List(ctx, workitem.ListFilter{Tag: "area:cli", ParentID: "sty_parent01"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != multi.ID {
		t.Errorf("area:cli+parent = %+v, want multi only", got)
	}

	// Unknown tag → empty
	got, err = db.Stories.List(ctx, workitem.ListFilter{Tag: "sprint:99"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("unknown tag returned %d items", len(got))
	}
}

func TestLedgerAppendAndList(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	now := time.Now()

	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: "sty_abc", Kind: ledger.KindStoryCreated, Body: "created",
	}, now); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: "sty_abc", Kind: ledger.KindComment, Body: "note",
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	entries, err := db.Ledger.ListByStory(ctx, "sty_abc", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Kind != ledger.KindStoryCreated || entries[1].Kind != ledger.KindComment {
		t.Errorf("entries not oldest-first: %v, %v", entries[0].Kind, entries[1].Kind)
	}
	if entries[0].ID[:4] != "evt_" {
		t.Errorf("ledger id = %q, want evt_ prefix", entries[0].ID)
	}

	// Filtered by kind.
	comments, _ := db.Ledger.List(ctx, ledger.ListFilter{StoryID: "sty_abc", Kind: ledger.KindComment})
	if len(comments) != 1 {
		t.Errorf("comment-filtered = %d, want 1", len(comments))
	}

	// Unfiltered list is refused.
	if _, err := db.Ledger.List(ctx, ledger.ListFilter{}); err == nil {
		t.Error("unfiltered list should be refused")
	}
}

// TestNoDriftingDocsPrincipleOverlayAndShadow: opened through Open, so the
// production embedded-default wiring is what is exercised. With no file on disk
// the shipped principle resolves and lists once; a same-named authored file
// shadows it, still listed once (sty_9fa1023d).
func TestNoDriftingDocsPrincipleOverlayAndShadow(t *testing.T) {
	const name = "satelle-no-drifting-docs"
	ctx := context.Background()
	root := t.TempDir()
	db, err := Open(filepath.Join(root, ".satelle", "satelle.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	dir := filepath.Join(root, ".satelle", "principles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dirs := map[string]string{"principles": dir}

	var shipped string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "principles" && d.Name == name {
			shipped = d.Body
		}
	}
	if shipped == "" {
		t.Fatalf("%s is not in the embedded defaults", name)
	}

	// listed returns the body of every listed doc named name.
	listed := func() []string {
		t.Helper()
		docs, err := db.DocIndex.List(ctx, "principles")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		var bodies []string
		for _, d := range docs {
			if d.Name == name {
				bodies = append(bodies, d.Body)
			}
		}
		return bodies
	}

	// Overlay: nothing on disk, so the shipped bytes resolve and list once.
	if _, err := db.DocIndex.Sync(ctx, dirs, time.Now().UTC()); err != nil {
		t.Fatalf("Sync (empty): %v", err)
	}
	got, err := db.DocIndex.Get(ctx, "principles", name)
	if err != nil {
		t.Fatalf("Get overlay: %v", err)
	}
	if got.Body != shipped {
		t.Errorf("overlay Get body is not the shipped principle")
	}
	if bodies := listed(); len(bodies) != 1 || bodies[0] != shipped {
		t.Errorf("overlay List = %d entries, want the shipped principle once", len(bodies))
	}

	// Shadow: an authored file of the same name wins, and is listed once.
	const authored = "---\nname: satelle-no-drifting-docs\ntype: principle\ntags: [type:principle]\napplies_to: [\"*\"]\ndescription: operator override\n---\n\n# operator body\n\nOPERATOR-OVERRIDE\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(authored), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DocIndex.Sync(ctx, dirs, time.Now().UTC()); err != nil {
		t.Fatalf("Sync (authored): %v", err)
	}
	got, err = db.DocIndex.Get(ctx, "principles", name)
	if err != nil {
		t.Fatalf("Get shadow: %v", err)
	}
	if !strings.Contains(got.Body, "OPERATOR-OVERRIDE") || got.Body == shipped {
		t.Errorf("authored file did not shadow the shipped principle: %q", got.Body)
	}
	if bodies := listed(); len(bodies) != 1 || !strings.Contains(bodies[0], "OPERATOR-OVERRIDE") {
		t.Errorf("shadow List = %d entries, want the authored principle once", len(bodies))
	}
}
