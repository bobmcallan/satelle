package snapsync

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
)

func TestOfflineDriftReadsTheRecordedBase(t *testing.T) {
	hosted.DocumentSyncStatePathOverride = filepath.Join(t.TempDir(), "state.json")
	t.Cleanup(func() { hosted.DocumentSyncStatePathOverride = "" })
	data := t.TempDir()
	const server, project, repo = "https://example.test", "p", "/repo"

	d, err := OfflineDrift(server, project, repo, data, "skills", map[string]string{"skills/a.md": "1"})
	if err != nil || d.Synced || !reflect.DeepEqual(d.Changed, []string{"skills/a.md"}) {
		t.Fatalf("never synced: %+v %v", d, err)
	}

	base := hosted.AreaBase{Version: 3, Files: map[string]string{"skills/a.md": "1", "skills/b.md": "2"}, Unmerged: map[string]string{"skills/b.md": "9"}}
	if err := hosted.SaveAreaBase(server, project, repo, "skills", base); err != nil {
		t.Fatal(err)
	}
	local := map[string]string{"skills/a.md": "changed", "skills/c.md": "3"}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(data, filepath.FromSlash(CopyPath("skills", "skills/b.md")))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, filepath.FromSlash(CopyPath("skills", "skills/b.md"))), []byte("theirs"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err = OfflineDrift(server, project, repo, data, "skills", local)
	if err != nil || !d.Synced || d.Version != 3 {
		t.Fatalf("synced: %+v %v", d, err)
	}
	// Edited, added, and deleted-since-base all count as changed.
	if want := []string{"skills/a.md", "skills/b.md", "skills/c.md"}; !reflect.DeepEqual(d.Changed, want) {
		t.Errorf("Changed = %v, want %v", d.Changed, want)
	}
	if want := []string{"skills/b.md"}; !reflect.DeepEqual(d.Unmerged, want) {
		t.Errorf("Unmerged = %v, want %v (the conflict copy is on disk)", d.Unmerged, want)
	}

	// The operator deleting the copy says "merged": no longer unmerged, and the
	// base is not rewritten by merely looking.
	if err := os.Remove(filepath.Join(data, filepath.FromSlash(CopyPath("skills", "skills/b.md")))); err != nil {
		t.Fatal(err)
	}
	d, _ = OfflineDrift(server, project, repo, data, "skills", local)
	if len(d.Unmerged) != 0 {
		t.Errorf("Unmerged = %v after the copy was deleted", d.Unmerged)
	}
	if stored, _, _ := hosted.LoadAreaBase(server, project, repo, "skills"); len(stored.Unmerged) != 1 {
		t.Errorf("OfflineDrift must not write the base: %+v", stored)
	}
}
