package hosted

import (
	"path/filepath"
	"testing"
)

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
