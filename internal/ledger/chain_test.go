package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func memDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func appendN(t *testing.T, s *Store, story string, n int) []Entry {
	t.Helper()
	var out []Entry
	for i := 0; i < n; i++ {
		e, err := s.Append(context.Background(), AppendInput{StoryID: story, Kind: "noise", Body: fmt.Sprint(i)}, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// AC1: an entry names its story's previous entry; a story's first and a
// story-less entry have no base.
func TestAppendLinksStoryChain(t *testing.T) {
	s := New(memDB(t, "chainlink"))
	a := appendN(t, s, "sty_a", 3)
	b := appendN(t, s, "sty_b", 1)
	free, err := s.Append(context.Background(), AppendInput{Kind: "noise"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if a[0].BaseID != "" || a[1].BaseID != a[0].ID || a[2].BaseID != a[1].ID {
		t.Errorf("sty_a bases = %q %q %q", a[0].BaseID, a[1].BaseID, a[2].BaseID)
	}
	if b[0].BaseID != "" {
		t.Errorf("another story's first entry has base %q", b[0].BaseID)
	}
	if free.BaseID != "" {
		t.Errorf("a story-less entry has base %q", free.BaseID)
	}
	// Interleaving another story does not disturb the chain; the stored row agrees
	// with the returned entry.
	appendN(t, s, "sty_b", 1)
	last := appendN(t, s, "sty_a", 1)[0]
	got, ok, err := s.GetByID(context.Background(), last.ID)
	if err != nil || !ok || got.BaseID != a[2].ID {
		t.Errorf("stored base = %q (ok=%v err=%v), want %q", got.BaseID, ok, err, a[2].ID)
	}
	rep, err := s.VerifyAll(context.Background())
	if err != nil || len(rep.Breaks) != 0 || rep.Stories != 2 {
		t.Errorf("verify = %+v, %v", rep, err)
	}
}

func TestAppendOnceLinksChain(t *testing.T) {
	s := New(memDB(t, "chainonce"))
	ctx := context.Background()
	first := appendN(t, s, "sty_a", 1)[0]
	e, ok, err := s.AppendOnce(ctx, AppendInput{StoryID: "sty_a", Kind: "claim", Refs: json.RawMessage(`["x"]`)}, time.Time{})
	if err != nil || !ok || e.BaseID != first.ID {
		t.Fatalf("append once = %+v ok=%v err=%v", e, ok, err)
	}
	dup, ok, err := s.AppendOnce(ctx, AppendInput{StoryID: "sty_a", Kind: "claim", Refs: json.RawMessage(`["x"]`)}, time.Time{})
	if err != nil || ok {
		t.Fatalf("duplicate append once = %+v ok=%v err=%v", dup, ok, err)
	}
	if n := countStory(t, s, "sty_a"); n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}
}

func countStory(t *testing.T, s *Store, story string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM evidence WHERE story_id = ?`, story).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// AC2: concurrent appends to one story never share a base; the chain verifies.
func TestConcurrentAppendsFormOneChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "race.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(8)
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	const workers, each = 8, 20
	var wg sync.WaitGroup
	errs := make(chan error, workers*each)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if _, err := s.Append(context.Background(), AppendInput{StoryID: "sty_race", Kind: "noise"}, time.Time{}); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	rep, err := s.VerifyStory(context.Background(), "sty_race")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Events != workers*each || len(rep.Breaks) != 0 {
		t.Errorf("events=%d breaks=%+v, want %d and none", rep.Events, rep.Breaks, workers*each)
	}
}

// AC3 (store level): the resolver stamps the hash; a given hash is kept; no
// story and no resolver stamp nothing.
func TestAppendStampsWorkflowHash(t *testing.T) {
	s := New(memDB(t, "chainhash"))
	ctx := context.Background()
	if e := appendN(t, s, "sty_a", 1)[0]; e.WorkflowHash != "" {
		t.Errorf("no resolver set, hash = %q", e.WorkflowHash)
	}
	var seen []string
	s.SetWorkflowVersion(func(_ context.Context, c Conn, story string) string {
		seen = append(seen, story)
		if story == "sty_none" {
			return ""
		}
		return "hash-of-" + story
	})
	e := appendN(t, s, "sty_a", 1)[0]
	if e.WorkflowHash != "hash-of-sty_a" {
		t.Errorf("hash = %q", e.WorkflowHash)
	}
	if got, _, _ := s.GetByID(ctx, e.ID); got.WorkflowHash != "hash-of-sty_a" {
		t.Errorf("stored hash = %q", got.WorkflowHash)
	}
	given, err := s.Append(ctx, AppendInput{StoryID: "sty_a", Kind: "noise", WorkflowHash: "explicit"}, time.Time{})
	if err != nil || given.WorkflowHash != "explicit" {
		t.Errorf("explicit hash = %q, %v", given.WorkflowHash, err)
	}
	if none := appendN(t, s, "sty_none", 1)[0]; none.WorkflowHash != "" {
		t.Errorf("no workflow applies, hash = %q", none.WorkflowHash)
	}
	free, _ := s.Append(ctx, AppendInput{Kind: "noise"}, time.Time{})
	if free.WorkflowHash != "" || len(seen) != 2 { // not asked for a given hash, nor for a story-less entry
		t.Errorf("story-less hash = %q, resolver calls = %v", free.WorkflowHash, seen)
	}
	// A copy bound to a transaction keeps the resolver.
	tx, err := s.db.(*sql.DB).Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	in, err := s.WithTx(tx).Append(ctx, AppendInput{StoryID: "sty_a", Kind: "noise"}, time.Time{})
	if err != nil || in.WorkflowHash != "hash-of-sty_a" {
		t.Errorf("in-tx hash = %q, %v", in.WorkflowHash, err)
	}
}

// AC4: rows that predate the columns are linked once, in insertion order, and
// nothing else changes.
func TestMigrateLinksLegacyChains(t *testing.T) {
	db, err := sql.Open("sqlite", "file:chainlegacy?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
CREATE TABLE evidence (
    id         TEXT PRIMARY KEY,
    story_id   TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    kind       TEXT NOT NULL,
    actor      TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL DEFAULT '',
    payload    TEXT NOT NULL DEFAULT '{}',
    refs       TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL
)`); err != nil {
		t.Fatal(err)
	}
	// Insertion order is deliberately not created_at order for sty_a.
	rows := []struct{ id, story, at string }{
		{"e1", "sty_a", "2026-01-03T00:00:00Z"},
		{"e2", "sty_b", "2026-01-01T00:00:00Z"},
		{"e3", "sty_a", "2026-01-02T00:00:00Z"},
		{"e4", "", "2026-01-01T00:00:00Z"},
		{"e5", "sty_a", "2026-01-01T00:00:00Z"},
		{"e6", "sty_b", "2026-01-04T00:00:00Z"},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO evidence (id, story_id, kind, actor, body, payload, refs, created_at)
            VALUES (?, ?, 'noise', 'who', 'body-'||?, '{"k":"'||?||'"}', '["r"]', ?)`,
			r.id, r.story, r.id, r.id, r.at); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() string {
		rs, err := db.Query(`SELECT id, story_id, project_id, kind, actor, body, payload, refs, created_at FROM evidence ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		defer rs.Close()
		var b strings.Builder
		for rs.Next() {
			var c [9]string
			if err := rs.Scan(&c[0], &c[1], &c[2], &c[3], &c[4], &c[5], &c[6], &c[7], &c[8]); err != nil {
				t.Fatal(err)
			}
			b.WriteString(strings.Join(c[:], "|") + "\n")
		}
		return b.String()
	}
	before := snapshot()
	for i := 0; i < 2; i++ {
		if err := Migrate(db); err != nil {
			t.Fatalf("migrate %d: %v", i, err)
		}
	}
	if after := snapshot(); after != before {
		t.Errorf("a column other than base_id changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	want := map[string]string{"e1": "", "e2": "", "e3": "e1", "e4": "", "e5": "e3", "e6": "e2"}
	for id, base := range want {
		var got, hash string
		if err := db.QueryRow(`SELECT base_id, workflow_hash FROM evidence WHERE id = ?`, id).Scan(&got, &hash); err != nil {
			t.Fatal(err)
		}
		if got != base || hash != "" {
			t.Errorf("%s: base=%q hash=%q, want base %q and no hash", id, got, hash, base)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM evidence`).Scan(&n); err != nil || n != len(rows) {
		t.Errorf("rows = %d, %v; want %d", n, err, len(rows))
	}
	// Linked once: a row appended afterwards with no base stays a root.
	if _, err := db.Exec(`INSERT INTO evidence (id, story_id, kind, created_at) VALUES ('late', 'sty_a', 'noise', '2026-02-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var late string
	if err := db.QueryRow(`SELECT base_id FROM evidence WHERE id = 'late'`).Scan(&late); err != nil || late != "" {
		t.Errorf("a later migrate relinked a row: base=%q err=%v", late, err)
	}
	// The migrated chain verifies.
	rep, err := New(db).VerifyStory(context.Background(), "sty_b")
	if err != nil || len(rep.Breaks) != 0 {
		t.Errorf("verify sty_b = %+v, %v", rep, err)
	}
}

// AC5: Upsert keeps a base and hash it arrives with and links a baseless entry
// to the story's tail.
func TestUpsertKeepsSuppliedBase(t *testing.T) {
	s := New(memDB(t, "chainupsertkeep"))
	ctx := context.Background()
	appendN(t, s, "sty_a", 2)
	in := Entry{ID: "evt_given", StoryID: "sty_a", Kind: "noise", BaseID: "evt_elsewhere", WorkflowHash: "wh"}
	out, err := s.Upsert(ctx, in, time.Time{})
	if err != nil || out.BaseID != "evt_elsewhere" || out.WorkflowHash != "wh" {
		t.Fatalf("upsert = %+v, %v", out, err)
	}
	got, ok, err := s.GetByID(ctx, "evt_given")
	if err != nil || !ok || got.BaseID != "evt_elsewhere" || got.WorkflowHash != "wh" {
		t.Errorf("stored = %+v ok=%v err=%v", got, ok, err)
	}
}

func TestUpsertLinksBaselessEntry(t *testing.T) {
	s := New(memDB(t, "chainupsertlink"))
	ctx := context.Background()
	tail := appendN(t, s, "sty_a", 2)[1]
	out, err := s.Upsert(ctx, Entry{ID: "evt_new", StoryID: "sty_a", Kind: "noise"}, time.Time{})
	if err != nil || out.BaseID != tail.ID {
		t.Fatalf("upsert base = %q, %v; want %q", out.BaseID, err, tail.ID)
	}
	// Re-running writes nothing and does not move the stored base.
	appendN(t, s, "sty_a", 1)
	again, err := s.Upsert(ctx, Entry{ID: "evt_new", StoryID: "sty_a", Kind: "noise"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.GetByID(ctx, "evt_new"); got.BaseID != tail.ID || again.BaseID != "" {
		t.Errorf("stored base %q, re-upsert returned base %q", got.BaseID, again.BaseID)
	}
	free, err := s.Upsert(ctx, Entry{ID: "evt_free", Kind: "noise"}, time.Time{})
	if err != nil || free.BaseID != "" {
		t.Errorf("story-less upsert base = %q, %v", free.BaseID, err)
	}
}

func ev(id, base string) Entry { return Entry{ID: id, StoryID: "sty_v", Kind: "noise", BaseID: base} }

func breakKinds(bs []ChainBreak) []string {
	var out []string
	for _, b := range bs {
		out = append(out, b.EventID+":"+b.Kind)
	}
	return out
}

// AC6: each break kind, and the fork-recorded case.
func TestVerifyChainBreaks(t *testing.T) {
	cases := []struct {
		name    string
		entries []Entry
		want    []string
	}{
		{"empty", nil, nil},
		{"linear", []Entry{ev("a", ""), ev("b", "a"), ev("c", "b")}, nil},
		{"missing base", []Entry{ev("a", ""), ev("b", "ghost")}, []string{"b:missing_base"}},
		{"base after event", []Entry{ev("a", ""), ev("b", "c"), ev("c", "a")}, []string{"b:base_after_event"}},
		{"self base", []Entry{ev("a", ""), ev("b", "b")}, []string{"b:base_after_event"}},
		{"two roots", []Entry{ev("a", ""), ev("b", "a"), ev("c", "")}, []string{"c:extra_root"}},
		{"unrecorded fork", []Entry{ev("a", ""), ev("b", "a"), ev("c", "a")}, []string{"c:fork"}},
		{"three-way fork", []Entry{ev("a", ""), ev("b", "a"), ev("c", "a"), ev("d", "a")}, []string{"c:fork", "d:fork"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := breakKinds(VerifyChain(c.entries))
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("breaks = %v, want %v", got, c.want)
			}
		})
	}

	fork := func(refs string) Entry {
		return Entry{ID: "f", StoryID: "sty_v", Kind: KindChainFork, BaseID: "c", Refs: json.RawMessage(refs)}
	}
	recorded := []Entry{ev("a", ""), ev("b", "a"), ev("c", "a"), fork(`["b","c"]`)}
	if got := VerifyChain(recorded); len(got) != 0 {
		t.Errorf("a fork named by a chain_fork record is a break: %v", got)
	}
	partial := []Entry{ev("a", ""), ev("b", "a"), ev("c", "a"), ev("d", "a"), fork(`["b","c"]`)}
	if got := breakKinds(VerifyChain(partial)); strings.Join(got, ",") != "c:fork,d:fork" {
		t.Errorf("a record naming only some of the events must not excuse the group: %v", got)
	}
}

func TestVerifyIsReadOnlyAndReportsBrokenStoreChain(t *testing.T) {
	db := memDB(t, "chainverifystore")
	s := New(db)
	ctx := context.Background()
	es := appendN(t, s, "sty_a", 3)
	appendN(t, s, "sty_ok", 2)
	if _, err := db.Exec(`UPDATE evidence SET base_id = 'ghost' WHERE id = ?`, es[1].ID); err != nil {
		t.Fatal(err)
	}
	var before int
	_ = db.QueryRow(`SELECT COUNT(*) FROM evidence`).Scan(&before)
	rep, err := s.VerifyAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Stories != 2 || rep.Events != 5 || strings.Join(breakKinds(rep.Breaks), ",") != es[1].ID+":missing_base" {
		t.Errorf("report = %+v", rep)
	}
	if rep.Breaks[0].StoryID != "sty_a" {
		t.Errorf("break story = %q", rep.Breaks[0].StoryID)
	}
	one, err := s.VerifyStory(ctx, "sty_ok")
	if err != nil || len(one.Breaks) != 0 || one.Breaks == nil {
		t.Errorf("sty_ok = %+v, %v", one, err)
	}
	var base string
	_ = db.QueryRow(`SELECT base_id FROM evidence WHERE id = ?`, es[1].ID).Scan(&base)
	var after int
	_ = db.QueryRow(`SELECT COUNT(*) FROM evidence`).Scan(&after)
	if base != "ghost" || after != before {
		t.Errorf("verify modified the ledger: base=%q rows %d→%d", base, before, after)
	}
	if _, err := s.VerifyStory(ctx, " "); err == nil {
		t.Error("verify with no story id should be refused")
	}
}
