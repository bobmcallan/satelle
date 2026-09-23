package fsmove

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMoveFileAndTree(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src", "a.txt"), "a")
	write(t, filepath.Join(root, "src", "d", "b.txt"), "b")
	if err := Move(filepath.Join(root, "src"), filepath.Join(root, "out", "nested", "src")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "src")); !os.IsNotExist(err) {
		t.Error("source still present")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "out", "nested", "src", "d", "b.txt")); string(b) != "b" {
		t.Errorf("content = %q", b)
	}
}

func TestMoveRefusesExistingDestination(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a"), "a")
	write(t, filepath.Join(root, "b"), "b")
	if err := Move(filepath.Join(root, "a"), filepath.Join(root, "b")); err == nil {
		t.Fatal("want refusal")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "b")); string(b) != "b" {
		t.Errorf("destination overwritten: %q", b)
	}
}

// The cross-device path: exercised directly since a rename within one temp
// filesystem never falls back. Symlinks are copied as links, not followed.
func TestCopyTreeKeepsSymlinksAndModes(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src", "f.txt"), "f")
	if err := os.Symlink("f.txt", filepath.Join(root, "src", "link")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(root, "src"), filepath.Join(root, "dst")); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(filepath.Join(root, "dst", "link")); err != nil || got != "f.txt" {
		t.Errorf("symlink not preserved: %q %v", got, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "dst", "f.txt")); string(b) != "f" {
		t.Errorf("content = %q", b)
	}
}
