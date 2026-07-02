package verb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestMigrateLegacySummaries covers the one-time adoption (sty_97c53d72):
// legacy summaries move from the retired documents sub-bundle (and documents
// root) into the owning story's attachment folder; the emptied sub-bundle is
// removed; a file with no story id in its name stays put.
func TestMigrateLegacySummaries(t *testing.T) {
	data := t.TempDir()
	stories := filepath.Join(data, "stories")
	docs := filepath.Join(data, "documents")
	sub := filepath.Join(docs, "story-implementation-summary")
	for _, d := range []string{stories, sub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w := func(p, body string) {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w(filepath.Join(sub, "commit-summary-sty_aaaa1111.md"), "# A")
	w(filepath.Join(sub, "index.md"), "# idx")
	w(filepath.Join(sub, "log.md"), "# log")
	w(filepath.Join(docs, "commit-summary-sty_bbbb2222.md"), "# B")
	w(filepath.Join(docs, "commit-summary-noid.md"), "# no id")

	migrateLegacySummaries(stories)

	if _, err := os.Stat(filepath.Join(stories, "sty_aaaa1111", "commit-summary-sty_aaaa1111.md")); err != nil {
		t.Errorf("sub-bundle summary not migrated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stories, "sty_bbbb2222", "commit-summary-sty_bbbb2222.md")); err != nil {
		t.Errorf("root summary not migrated: %v", err)
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Errorf("emptied sub-bundle should be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(docs, "commit-summary-noid.md")); err != nil {
		t.Errorf("id-less file must stay put: %v", err)
	}
	migrateLegacySummaries(stories) // idempotent
}

// TestSyncStoriesReport covers the artifact review (sty_8f7b2157): orphaned
// artifact dirs and misfiled artifacts are REPORTED (never deleted); counts fill
// the report. Uses a real store so the DB-backed check is exercised.
func TestSyncStoriesReport(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := t.TempDir()
	SetStoryDir(dir)
	defer SetStoryDir("")

	ctx := context.Background()
	now := time.Now()
	it, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "S", Category: "feature"}, now)
	if err != nil {
		t.Fatal(err)
	}
	// A valid artifact + a misfiled one under the real story; a whole orphaned dir.
	w := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w(filepath.Join(dir, it.ID, "good.md"), "---\nstory: "+it.ID+"\ntype: evidence\n---\n\n# ok\n")
	w(filepath.Join(dir, it.ID, "misfiled.md"), "---\nstory: sty_deadbeef\ntype: evidence\n---\n\n# wrong\n")
	w(filepath.Join(dir, "sty_00000000", "old.md"), "---\nstory: sty_00000000\n---\n\n# orphan\n")

	rep, err := SyncStories(ctx, db.Stories, now)
	if err != nil {
		t.Fatalf("SyncStories: %v", err)
	}
	if rep.Materialized != 1 || rep.ArtifactDirs != 2 {
		t.Errorf("report = %+v, want materialized=1 artifactDirs=2", rep)
	}
	// Pruned counts stale views: plant one and re-sync.
	if err := os.WriteFile(filepath.Join(dir, "sty_ffffffff.md"), []byte("# stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep2, err := SyncStories(ctx, db.Stories, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Pruned != 1 {
		t.Errorf("pruned = %d, want 1 (the planted stale view)", rep2.Pruned)
	}
	if len(rep.Orphaned) != 1 || rep.Orphaned[0] != "sty_00000000" {
		t.Errorf("orphaned = %v", rep.Orphaned)
	}
	if len(rep.Problems) != 1 || !strings.Contains(rep.Problems[0], "misfiled.md") || !strings.Contains(rep.Problems[0], "sty_deadbeef") {
		t.Errorf("problems = %v", rep.Problems)
	}
	// Nothing deleted: evidence is authored.
	for _, p := range []string{filepath.Join(dir, it.ID, "misfiled.md"), filepath.Join(dir, "sty_00000000", "old.md")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("sync must not delete artifacts: %s gone (%v)", p, err)
		}
	}
}
