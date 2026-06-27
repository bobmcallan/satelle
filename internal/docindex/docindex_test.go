package docindex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSyncIndexesAndExtractsHeadline(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	docsDir := filepath.Join(t.TempDir(), "documents")
	write(t, docsDir, "intro.md", "# Welcome\n\nbody text")
	write(t, docsDir, "notes.txt", "ignored — not markdown")

	dirs := map[string]string{"documents": docsDir}
	res, err := s(db).Sync(ctx, dirs, time.Now())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Indexed != 1 || res.Scanned != 1 {
		t.Errorf("res = %+v, want Indexed=1 Scanned=1 (txt ignored)", res)
	}
	doc, err := s(db).Get(ctx, "documents", "intro")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if doc.Headline != "Welcome" {
		t.Errorf("headline = %q, want Welcome", doc.Headline)
	}
	if doc.Hash == "" {
		t.Error("hash not set")
	}
}

func TestSyncSkipsYAMLFrontmatterInHeadline(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	docsDir := filepath.Join(t.TempDir(), "workflows")
	write(t, docsDir, "wf.md", "---\nname: wf\ntags: [kind:workflow]\n---\n\n# Real Heading\n\nbody")
	if _, err := s(db).Sync(ctx, map[string]string{"workflows": docsDir}, time.Now()); err != nil {
		t.Fatal(err)
	}
	doc, err := s(db).Get(ctx, "workflows", "wf")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Headline != "Real Heading" {
		t.Errorf("headline = %q, want Real Heading (frontmatter skipped)", doc.Headline)
	}
}

func TestSyncSkipsUnchangedAndReindexesChanged(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	docsDir := filepath.Join(t.TempDir(), "documents")
	p := write(t, docsDir, "a.md", "v1")
	dirs := map[string]string{"documents": docsDir}

	if _, err := s(db).Sync(ctx, dirs, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Second pass with no change → nothing reindexed.
	res, _ := s(db).Sync(ctx, dirs, time.Now())
	if res.Indexed != 0 {
		t.Errorf("unchanged reindexed: %+v", res)
	}

	// Modify the file with a clearly newer mtime → reindexed.
	if err := os.WriteFile(p, []byte("v2 longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	res, _ = s(db).Sync(ctx, dirs, time.Now())
	if res.Indexed != 1 {
		t.Errorf("changed not reindexed: %+v", res)
	}
	doc, _ := s(db).Get(ctx, "documents", "a")
	if doc.Body != "v2 longer" {
		t.Errorf("body = %q, want v2 longer", doc.Body)
	}
}

func TestSyncPrunesDeleted(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	docsDir := filepath.Join(t.TempDir(), "documents")
	p := write(t, docsDir, "gone.md", "x")
	dirs := map[string]string{"documents": docsDir}

	if _, err := s(db).Sync(ctx, dirs, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	res, _ := s(db).Sync(ctx, dirs, time.Now())
	if res.Pruned != 1 {
		t.Errorf("pruned = %d, want 1", res.Pruned)
	}
	if _, err := s(db).Get(ctx, "documents", "gone"); err != ErrNotFound {
		t.Errorf("get after prune err = %v, want ErrNotFound", err)
	}
}

func TestSyncMissingDirIsBenign(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	dirs := map[string]string{"workflows": filepath.Join(t.TempDir(), "does-not-exist")}
	res, err := s(db).Sync(ctx, dirs, time.Now())
	if err != nil {
		t.Fatalf("missing dir errored: %v", err)
	}
	if res.Scanned != 0 || res.Indexed != 0 {
		t.Errorf("res = %+v, want all zero", res)
	}
}

func s(db *sql.DB) *Store { return New(db) }

func TestSyncReportsChanged(t *testing.T) {
	dir := t.TempDir()
	wf := filepath.Join(dir, "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wf, "a.md"), []byte("# a"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	s := New(db)
	res, err := s.Sync(context.Background(), map[string]string{"workflows": wf}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 1 || res.Changed[0].Kind != "workflows" || res.Changed[0].Name != "a" {
		t.Fatalf("Changed = %+v, want one workflows/a", res.Changed)
	}
	// A second sync with no change reports nothing changed.
	res2, _ := s.Sync(context.Background(), map[string]string{"workflows": wf}, time.Now())
	if len(res2.Changed) != 0 {
		t.Fatalf("unchanged sync should report no Changed, got %+v", res2.Changed)
	}
}
