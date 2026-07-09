package subsync

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRestoreByteExact: a deploy's files round-trip byte-for-byte (0o644),
// nested dirs created as needed (AC2 byte-exact restore).
func TestRestoreByteExact(t *testing.T) {
	dataDir := t.TempDir()
	files := []File{
		{Path: "skills/team-review.md", Content: []byte("---\ntype: skill\n---\nbody\n")},
		{Path: "agents.toml", Content: []byte("[executor]\nharness = \"in-loop\"\n")},
		{Path: "tasks/tsk_abc.md", Content: []byte("nested parent dir")},
	}
	n, err := Restore(dataDir, files)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if n != len(files) {
		t.Fatalf("wrote %d, want %d", n, len(files))
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatalf("read back %s: %v", f.Path, err)
		}
		if string(got) != string(f.Content) {
			t.Errorf("%s bytes diverged: got %q want %q", f.Path, got, f.Content)
		}
	}
}

// TestRestoreOverwritesExisting: a deploy overwrites a file already on disk,
// matching the latest version byte-for-byte rather than merging.
func TestRestoreOverwritesExisting(t *testing.T) {
	dataDir := t.TempDir()
	dest := filepath.Join(dataDir, "constitution.md")
	if err := os.WriteFile(dest, []byte("OLD"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(dataDir, []File{{Path: "constitution.md", Content: []byte("NEW")}}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "NEW" {
		t.Errorf("constitution.md = %q, want NEW (overwrite)", got)
	}
}

// TestRestoreRefusesUnsafePath: a hostile manifest segment ("..") must not
// escape the data dir, and a local-only path must be refused outright.
func TestRestoreRefusesUnsafePath(t *testing.T) {
	dataDir := t.TempDir()
	bad := []File{
		{Path: "../escape.md", Content: []byte("x")},
		{Path: "satelle.db", Content: []byte("x")},
		{Path: "stories/x.md", Content: []byte("x")},
	}
	for _, f := range bad {
		if _, err := Restore(dataDir, []File{f}); err == nil {
			t.Errorf("Restore(%q) unexpectedly succeeded", f.Path)
		}
	}
	// Nothing was written outside the data dir.
	if _, err := os.Stat(filepath.Join(filepath.Dir(dataDir), "escape.md")); err == nil {
		t.Error("an unsafe path escaped the data dir")
	}
}

// TestCleanRel rejects the shapes a manifest must never carry.
func TestCleanRel(t *testing.T) {
	for _, bad := range []string{"", "/abs", `back\slash`, "a/../b", "a//b", "a/./b", "a\x00b"} {
		if _, err := cleanRel(bad); err == nil {
			t.Errorf("cleanRel(%q) unexpectedly succeeded", bad)
		}
	}
	for _, ok := range []string{"skills/x.md", "agents.toml", "tasks/sub/tsk_1.md"} {
		if _, err := cleanRel(ok); err != nil {
			t.Errorf("cleanRel(%q) unexpectedly failed: %v", ok, err)
		}
	}
}
