package verb

import (
	"os"
	"path/filepath"
	"testing"
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
