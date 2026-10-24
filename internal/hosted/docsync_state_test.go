package hosted

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sty_40853d02: the hold registry and pending claims are gone. A state file an
// earlier build wrote still carries them; it loads, its other state is kept, and
// the next write drops them.
func TestLegacyHoldStateIsIgnoredAndDroppedOnWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "document-sync-state.json")
	DocumentSyncStatePathOverride = path
	t.Cleanup(func() { DocumentSyncStatePathOverride = "" })
	const server, repo = "https://s.example", "/repo"
	key := documentCursorKey(server, "p", repo)
	legacy := `{"cursors":{"` + key + `":"c1"},` +
		`"holds":{"` + key + `":{"sty_a":{"location":"loc_other","label":"box"},"sty_b":"loc_legacy_string"}},` +
		`"pending":{"` + key + `":{"sty_c":"2026-10-01T00:00:00Z"}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadDocumentCursor(server, "p", repo); err != nil || got != "c1" {
		t.Fatalf("cursor = %q, %v; want c1 from a file carrying legacy holds", got, err)
	}
	if err := SaveStoryHead(server, "p", repo, "sty_a", "evt_1"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"holds"`) || strings.Contains(string(raw), `"pending"`) {
		t.Fatalf("rewritten state still carries hold records:\n%s", raw)
	}
	if got, _ := LoadDocumentCursor(server, "p", repo); got != "c1" {
		t.Fatalf("cursor after rewrite = %q, want c1", got)
	}
}

// sty_9bdf08c4: story heads live in the workstate cursor, per server, project and
// repo; the end-of-push cursor save carries none and must not wipe them.
func TestStoryHeadsSurviveACursorSaveAndStayPerKey(t *testing.T) {
	DocumentSyncStatePathOverride = filepath.Join(t.TempDir(), "document-sync-state.json")
	t.Cleanup(func() { DocumentSyncStatePathOverride = "" })
	const server, repo = "https://s.example", "/repo"
	if err := SaveStoryHead(server, "p", repo, "sty_a", "evt_1"); err != nil {
		t.Fatal(err)
	}
	if err := SaveStoryHead(server, "p", repo, "sty_b", "evt_2"); err != nil {
		t.Fatal(err)
	}
	if err := SaveStoryHead(server, "p", repo, "sty_a", "evt_3"); err != nil {
		t.Fatal(err)
	}
	if err := SaveWorkstateCursor(server, "p", repo, WorkstateCursor{LedgerSeq: 7}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadWorkstateCursor(server, "p", repo)
	if err != nil || got.LedgerSeq != 7 || got.StoryHeads["sty_a"] != "evt_3" || got.StoryHeads["sty_b"] != "evt_2" {
		t.Fatalf("cursor = %+v, %v; want seq 7 and both heads", got, err)
	}
	for _, other := range [][3]string{{server, "q", repo}, {"https://t.example", "p", repo}, {server, "p", "/other"}} {
		if c, _ := LoadWorkstateCursor(other[0], other[1], other[2]); len(c.StoryHeads) != 0 {
			t.Errorf("heads leaked to %v: %v", other, c.StoryHeads)
		}
	}
}

func TestDocumentCursorRoundTrip(t *testing.T) {
	dir := t.TempDir()
	DocumentSyncStatePathOverride = filepath.Join(dir, "document-sync-state.json")
	t.Cleanup(func() { DocumentSyncStatePathOverride = "" })

	// Missing → empty cursor, no error.
	got, err := LoadDocumentCursor("https://s.example", "proj-a", "/repo/a")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("empty store cursor = %q, want \"\"", got)
	}

	if err := SaveDocumentCursor("https://s.example", "proj-a", "/repo/a", "cur-1"); err != nil {
		t.Fatal(err)
	}
	got, err = LoadDocumentCursor("https://s.example", "proj-a", "/repo/a")
	if err != nil || got != "cur-1" {
		t.Fatalf("after save = %q, %v; want cur-1", got, err)
	}

	// Different repoRoot does not collide.
	got, err = LoadDocumentCursor("https://s.example", "proj-a", "/repo/b")
	if err != nil || got != "" {
		t.Fatalf("other repo = %q, %v; want empty", got, err)
	}
	if err := SaveDocumentCursor("https://s.example", "proj-a", "/repo/b", "cur-b"); err != nil {
		t.Fatal(err)
	}
	// First key still intact.
	got, _ = LoadDocumentCursor("https://s.example", "proj-a", "/repo/a")
	if got != "cur-1" {
		t.Errorf("repo a clobbered: %q", got)
	}
	got, _ = LoadDocumentCursor("https://s.example", "proj-a", "/repo/b")
	if got != "cur-b" {
		t.Errorf("repo b = %q, want cur-b", got)
	}
	// Different project does not collide on the same repoRoot.
	got, err = LoadDocumentCursor("https://s.example", "proj-b", "/repo/a")
	if err != nil || got != "" {
		t.Fatalf("other project = %q, %v; want empty", got, err)
	}
}
