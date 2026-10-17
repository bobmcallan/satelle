package docindex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadWorkflowDocs(t *testing.T) {
	defaults := []Doc{
		{Kind: "workflows", Name: "default", Body: "embedded-default"},
		{Kind: "workflows", Name: "other", Body: "embedded-other"},
		{Kind: "skills", Name: "x", Body: "not-a-workflow"},
	}
	byName := func(docs []Doc) map[string]Doc {
		m := map[string]Doc{}
		for _, d := range docs {
			m[d.Name] = d
		}
		return m
	}

	t.Run("unreadable dir yields only the sentinel", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "workflows")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		docs := ReadWorkflowDocs(file, defaults)
		if len(docs) != 1 || !docs[0].IsUnreadable() {
			t.Fatalf("want only the sentinel, got %+v", docs)
		}
	})

	t.Run("unreadable file yields only the sentinel", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads a chmod-000 file")
		}
		dir := t.TempDir()
		bad := filepath.Join(dir, "default.md")
		if err := os.WriteFile(bad, []byte("disk"), 0o000); err != nil {
			t.Fatal(err)
		}
		docs := ReadWorkflowDocs(dir, defaults)
		if len(docs) != 1 || !docs[0].IsUnreadable() {
			t.Fatalf("want only the sentinel, got %+v", docs)
		}
		if docs[0].Path != bad {
			t.Fatalf("sentinel names %q, want %q", docs[0].Path, bad)
		}
	})

	t.Run("empty and absent dirs yield the workflow defaults", func(t *testing.T) {
		for _, dir := range []string{t.TempDir(), filepath.Join(t.TempDir(), "absent"), ""} {
			docs := ReadWorkflowDocs(dir, defaults)
			if len(docs) != 2 || docs[0].Name != "default" || docs[1].Name != "other" {
				t.Fatalf("dir %q: want the two workflow defaults, got %+v", dir, docs)
			}
		}
	})

	t.Run("a disk doc shadows its default", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "default.md"), []byte("# disk\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		docs := byName(ReadWorkflowDocs(dir, defaults))
		if len(docs) != 2 {
			t.Fatalf("want 2 docs, got %+v", docs)
		}
		if docs["default"].Body != "# disk\n" || docs["default"].Embedded {
			t.Fatalf("disk must win, got %+v", docs["default"])
		}
		if docs["other"].Body != "embedded-other" {
			t.Fatalf("the unshadowed default must remain, got %+v", docs["other"])
		}
	})
}
