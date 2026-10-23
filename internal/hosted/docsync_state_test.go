package hosted

import (
	"path/filepath"
	"testing"
)

func TestHoldRegistryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	DocumentSyncStatePathOverride = filepath.Join(dir, "document-sync-state.json")
	t.Cleanup(func() { DocumentSyncStatePathOverride = "" })
	if err := RecordHold("https://s.example", "p", "/repo", "sty_a", HoldRecord{Location: "loc_self"}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadHolds("https://s.example", "p", "/repo")
	if err != nil || got["sty_a"].Location != "loc_self" {
		t.Fatalf("load = %v %v", got, err)
	}
	if err := ForgetHold("https://s.example", "p", "/repo", "sty_a"); err != nil {
		t.Fatal(err)
	}
	got, _ = LoadHolds("https://s.example", "p", "/repo")
	if _, ok := got["sty_a"]; ok {
		t.Fatalf("forgot id still present: %v", got)
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
