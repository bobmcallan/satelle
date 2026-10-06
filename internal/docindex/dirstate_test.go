package docindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// sty_d6e209aa AC1: absent and unreadable are different facts, decided in one
// place. A regular file where the dir should be is unreadable on every platform
// and for root alike — chmod-based fixtures pass wrongly as root.
func TestProbeDirAbsentReadableUnreadable(t *testing.T) {
	base := t.TempDir()
	if st, err := ProbeDir(filepath.Join(base, "missing")); st != DirAbsent || err != nil {
		t.Errorf("missing path = %v, %v; want DirAbsent, nil", st, err)
	}
	if st, err := ProbeDir(""); st != DirAbsent || err != nil {
		t.Errorf("blank path = %v, %v; want DirAbsent, nil", st, err)
	}
	dir := filepath.Join(base, "dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if st, err := ProbeDir(dir); st != DirReadable || err != nil {
		t.Errorf("directory = %v, %v; want DirReadable, nil", st, err)
	}
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, err := ProbeDir(file); st != DirUnreadable || err == nil {
		t.Errorf("regular file = %v, %v; want DirUnreadable with an error", st, err)
	}
}

// An unreadable workflows dir yields ONLY the sentinel: no stale index rows from
// an earlier readable pass, and no embedded overlay for any precedence rule to
// govern by.
func TestListWorkflowsUnreadableReturnsOnlyTheSentinel(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "workflows")
	write(t, dir, "done.toml", "[meta]\nname = \"done\"\n")
	s := New(openDB(t))
	s.SetRoots(map[string]string{"workflows": dir})
	s.SetDefaults([]Doc{{Kind: "workflows", Name: "done", Ext: ".toml", Body: "[meta]\nname = \"done\"\n"}})

	docs, err := s.List(ctx, "workflows")
	if err != nil || len(docs) != 1 || docs[0].IsUnreadable() {
		t.Fatalf("readable dir: %v, %v; want the one authored doc", docs, err)
	}

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	docs, err = s.List(ctx, "workflows")
	if err != nil {
		t.Fatal(err)
	}
	path, reason, ok := UnreadableOf(docs)
	if len(docs) != 1 || !ok || path != dir || reason == "" {
		t.Fatalf("unreadable dir: %+v; want only the sentinel naming %s", docs, dir)
	}
	if got := WithoutUnreadable(docs); len(got) != 0 {
		t.Errorf("WithoutUnreadable = %+v, want none", got)
	}

	// Absent is the old behaviour: no sentinel, the embedded default overlays.
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	docs, err = s.List(ctx, "workflows")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := UnreadableOf(docs); ok || len(docs) != 1 || !docs[0].Embedded {
		t.Fatalf("absent dir: %+v; want the embedded default and no sentinel", docs)
	}
}
