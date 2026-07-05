package subsync

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// writeFile writes rel (repo-relative, forward slash) under root, creating dirs.
func writeFile(t *testing.T, root, rel string, content []byte) {
	t.Helper()
	dest := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitInit makes root a git work tree and stages args (git add). No commit is
// needed — git ls-files reads the index, which `git add` populates.
func gitInit(t *testing.T, root string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t.t"},
		{"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func gitAdd(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root, "add"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add %v: %v\n%s", args, err, out)
	}
}

// TestBundleTrackedOnly proves AC5's guardrail at the bundle layer: only
// git-tracked authored files are collected, verbatim; local-only state — even a
// force-tracked satelle.local.toml — is excluded by the hard filter; paths are
// relative to .satelle/, sorted, with the top-level dir as Kind.
func TestBundleTrackedOnly(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)

	// Authored substrate (tracked). One CRLF file and one binary file prove
	// verbatim, binary-safe handling.
	writeFile(t, repo, ".satelle/constitution.md", []byte("# constitution\n"))
	writeFile(t, repo, ".satelle/skills/commit.md", []byte("commit\r\nskill\r\n"))
	writeFile(t, repo, ".satelle/documents/testdata/blob.bin", []byte{0x00, 0x01, 0x02, 0xff, 0x00})
	gitAdd(t, repo, ".satelle/constitution.md", ".satelle/skills", ".satelle/documents")

	// Local-only state that must NEVER be pushed. Untracked ones are excluded by
	// git ls-files; the force-tracked satelle.local.toml proves the hard filter is
	// not merely trusting gitignore.
	writeFile(t, repo, ".satelle/satelle.db", []byte("SQLITE"))
	writeFile(t, repo, ".satelle/satelle.db-wal", []byte("WAL"))
	writeFile(t, repo, ".satelle/logs/executor.log", []byte("log"))
	writeFile(t, repo, ".satelle/satelle.local.toml", []byte("overlay=true\n"))
	gitAdd(t, repo, "-f", ".satelle/satelle.local.toml")

	files, err := Bundle(repo)
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}

	got := map[string]hosted.SubstrateFile{}
	var paths []string
	for _, f := range files {
		got[f.Path] = f
		paths = append(paths, f.Path)
	}

	// Exactly the three authored files.
	want := []string{"constitution.md", "documents/testdata/blob.bin", "skills/commit.md"}
	sortedPaths := append([]string(nil), paths...)
	sort.Strings(sortedPaths)
	if !equalStrings(paths, sortedPaths) {
		t.Errorf("Bundle paths not sorted: %v", paths)
	}
	if !equalStrings(sortedPaths, want) {
		t.Fatalf("Bundle set = %v, want %v", sortedPaths, want)
	}

	// Verbatim bytes (CRLF + binary preserved).
	if !bytes.Equal(got["skills/commit.md"].Content, []byte("commit\r\nskill\r\n")) {
		t.Errorf("CRLF content not verbatim: %q", got["skills/commit.md"].Content)
	}
	if !bytes.Equal(got["documents/testdata/blob.bin"].Content, []byte{0x00, 0x01, 0x02, 0xff, 0x00}) {
		t.Errorf("binary content not verbatim: %v", got["documents/testdata/blob.bin"].Content)
	}
	// Kind = top-level dir; root file has none.
	if got["skills/commit.md"].Kind != "skills" {
		t.Errorf("kind = %q, want skills", got["skills/commit.md"].Kind)
	}
	if got["constitution.md"].Kind != "" {
		t.Errorf("root file kind = %q, want empty", got["constitution.md"].Kind)
	}
	// The guardrail: none of the local-only paths leaked in.
	for _, forbidden := range []string{"satelle.db", "satelle.db-wal", "logs/executor.log", "satelle.local.toml"} {
		if _, ok := got[forbidden]; ok {
			t.Errorf("local-only path %q must never be bundled (AC5)", forbidden)
		}
	}
}

// TestBundleNotAGitRepo errors clearly rather than silently returning nothing.
func TestBundleNotAGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	writeFile(t, repo, ".satelle/constitution.md", []byte("x"))
	if _, err := Bundle(repo); err == nil {
		t.Fatal("Bundle in a non-git dir should error")
	}
}

// TestRestoreByteExact writes a bundle byte-for-byte under .satelle/, creating
// nested dirs.
func TestRestoreByteExact(t *testing.T) {
	dst := t.TempDir()
	files := []hosted.SubstrateFile{
		{Path: "constitution.md", Content: []byte("# c\n")},
		{Path: "skills/commit.md", Content: []byte("a\r\nb")},
		{Path: "documents/testdata/blob.bin", Content: []byte{0, 1, 2, 0xff}},
	}
	n, err := Restore(dst, files)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if n != 3 {
		t.Fatalf("wrote %d files, want 3", n)
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(dst, ".satelle", filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatalf("read restored %s: %v", f.Path, err)
		}
		if !bytes.Equal(got, f.Content) {
			t.Errorf("restored %s = %q, want %q", f.Path, got, f.Content)
		}
	}
}

// TestRestoreRefusesUnsafe rejects a path escaping .satelle/ and a local-only path.
func TestRestoreRefusesUnsafe(t *testing.T) {
	dst := t.TempDir()
	for _, bad := range []string{"../escape.md", "/abs.md", "a/../../b.md", "satelle.db", "logs/x.log"} {
		if _, err := Restore(dst, []hosted.SubstrateFile{{Path: bad, Content: []byte("x")}}); err == nil {
			t.Errorf("Restore(%q) should be refused", bad)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
