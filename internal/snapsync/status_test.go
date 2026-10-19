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

	d, err := OfflineDrift(server, project, repo, data, "skills", map[string]string{"skills/a.md": "1"}, nil)
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
	d, err = OfflineDrift(server, project, repo, data, "skills", local, nil)
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
	d, _ = OfflineDrift(server, project, repo, data, "skills", local, nil)
	if len(d.Unmerged) != 0 {
		t.Errorf("Unmerged = %v after the copy was deleted", d.Unmerged)
	}
	if stored, _, _ := hosted.LoadAreaBase(server, project, repo, "skills"); len(stored.Unmerged) != 1 {
		t.Errorf("OfflineDrift must not write the base: %+v", stored)
	}
}

// A base-only unedited default is not a deletion: the local view never holds one
// (sty_04e8c778). A base-only authored file and an edited local file still are.
func TestOfflineDriftIgnoresABaseOnlyDefault(t *testing.T) {
	hosted.DocumentSyncStatePathOverride = filepath.Join(t.TempDir(), "state.json")
	t.Cleanup(func() { hosted.DocumentSyncStatePathOverride = "" })
	const server, project, repo = "https://example.test", "p", "/repo"
	base := hosted.AreaBase{Version: 2, Files: map[string]string{
		"tasks/tsk_default.md": "d1", "tasks/tsk_older.md": "o1", "tasks/tsk_gone.md": "g1", "tasks/tsk_mine.md": "m1",
	}, Defaults: map[string]string{"tasks/tsk_older.md": "o1"}}
	if err := hosted.SaveAreaBase(server, project, repo, "tasks", base); err != nil {
		t.Fatal(err)
	}
	defaults := map[string]map[string]bool{"tasks/tsk_default.md": {"d1": true}}

	d, err := OfflineDrift(server, project, repo, t.TempDir(), "tasks", map[string]string{"tasks/tsk_mine.md": "m1"}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tasks/tsk_gone.md"}; !reflect.DeepEqual(d.Changed, want) {
		t.Errorf("Changed = %v, want %v (defaults — projected or recorded — are not deletions)", d.Changed, want)
	}
	d, _ = OfflineDrift(server, project, repo, t.TempDir(), "tasks", map[string]string{"tasks/tsk_mine.md": "edited", "tasks/tsk_gone.md": "g1"}, defaults)
	if want := []string{"tasks/tsk_mine.md"}; !reflect.DeepEqual(d.Changed, want) {
		t.Errorf("Changed = %v, want %v (an edited local file still counts)", d.Changed, want)
	}
}

func TestDefaultSetHolds(t *testing.T) {
	var zero DefaultSet
	if zero.Holds("p", "s") || zero.Holds("p", "") {
		t.Error("the zero set holds nothing")
	}
	set := NewDefaultSet(map[string]map[string]bool{"p": {"s": true}}, &hosted.AreaBase{Defaults: map[string]string{"q": "t"}})
	for _, c := range []struct {
		path, sha string
		want      bool
	}{
		{"p", "s", true}, {"q", "t", true}, {"p", "t", false}, {"q", "s", false}, {"r", "s", false}, {"p", "", false},
	} {
		if got := set.Holds(c.path, c.sha); got != c.want {
			t.Errorf("Holds(%q, %q) = %v, want %v", c.path, c.sha, got, c.want)
		}
	}
	if !set.sameFilesExcept(map[string]string{"a": "1"}, map[string]string{"a": "1", "p": "s"}) {
		t.Error("a held default only one side names is not a difference")
	}
	if set.sameFilesExcept(map[string]string{"a": "1"}, map[string]string{"a": "1", "p": "other"}) {
		t.Error("an unheld sha for the same path is a difference")
	}
	if set.sameFilesExcept(map[string]string{"a": "2"}, map[string]string{"a": "1"}) {
		t.Error("an edited file is a difference")
	}
}
