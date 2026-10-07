package subsync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotRecordPathIsNeverRestoredIntoATree(t *testing.T) {
	// The record rides the config route but must never land in a tree, whatever
	// binary pulls it: backups/ is excluded on the restore side everywhere.
	for _, p := range []string{"backups/sync/skills.snapshot.json", "backups/sync/documents.snapshot.json"} {
		if !ExcludedLocal(p) {
			t.Errorf("ExcludedLocal(%q) = false; a snapshot record must never be restored", p)
		}
	}
	dir := t.TempDir()
	res, err := Restore(dir, []File{{Path: "backups/sync/skills.snapshot.json", Content: []byte("{}")}})
	if err != nil || res.Written != 0 || len(res.Skipped) != 1 {
		t.Fatalf("Restore = %+v, %v; want the record skipped", res, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "backups")); !os.IsNotExist(err) {
		t.Error("Restore created backups/")
	}
}

func TestWriteAsideOnlyWritesUnderTheTwoSyncDirs(t *testing.T) {
	dir := t.TempDir()
	rel := AsidePath(ConflictDir, "skills", "skills/x.md")
	if rel != "backups/sync-conflicts/skills/x.md" {
		t.Fatalf("AsidePath = %q", rel)
	}
	if err := WriteAside(dir, rel, []byte("remote")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil || string(got) != "remote" {
		t.Fatalf("aside copy = %q, %v", got, err)
	}
	if rel := AsidePath(RemovedDir, "settings", "satelle.toml"); rel != "backups/sync-removed/settings/satelle.toml" {
		t.Errorf("single-file area AsidePath = %q", rel)
	}
	for _, bad := range []string{
		"skills/x.md",                       // not under backups/
		"backups/other/x.md",                // backups/ but not a sync dir
		"backups/sync-conflicts/../../x.md", // escape
		"/etc/passwd",
		"backups/sync-conflicts/a\\b",
	} {
		if err := WriteAside(dir, bad, []byte("x")); err == nil {
			t.Errorf("WriteAside(%q) succeeded", bad)
		}
	}
}

func TestRemoveGuardsAndPrunesEmptyDirs(t *testing.T) {
	dir := t.TempDir()
	mk := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("skills/sub/a.md", "a")
	mk("skills/b.md", "b")
	mk("satelle.db", "live database")
	mk("backups/sync-conflicts/skills/keep.md", "parked")

	removed, err := Remove(dir, []string{"skills/sub/a.md", "skills/missing.md", "satelle.db", "backups/sync-conflicts/skills/keep.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "skills/sub/a.md" {
		t.Fatalf("removed = %v, want only skills/sub/a.md (missing is a no-op, local-only paths are never touched)", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "sub")); !os.IsNotExist(err) {
		t.Error("the emptied skills/sub directory was not pruned")
	}
	for _, kept := range []string{"skills/b.md", "satelle.db", "backups/sync-conflicts/skills/keep.md"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(kept))); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
	if _, err := os.Stat(dir); err != nil {
		t.Error("dataDir itself must never be pruned")
	}
	if _, err := Remove(dir, []string{"../escape.md"}); err == nil {
		t.Error("Remove accepted a path escaping the data dir")
	}
}
