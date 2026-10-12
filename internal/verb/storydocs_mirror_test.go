package verb_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
)

// TestStoryAttachmentMirrorHelpers (sty_4d42e13e): the helpers workstate sync uses
// to mirror attachments return markdown files raw, count binary ones without
// returning them, and write only bare markdown names under a bare story id.
func TestStoryAttachmentMirrorHelpers(t *testing.T) {
	withWiring(t)
	stories := filepath.Join(t.TempDir(), "stories")
	verb.SetStoryDir(stories)
	dir := filepath.Join(stories, "sty_aaaa1111")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := "---\nstory: sty_aaaa1111\ntype: plan\nname: plan\n---\n\nbody\n"
	for name, body := range map[string]string{
		"plan.md":                       raw,
		"shot.png":                      "\x89PNG",
		"shot.png" + verb.SidecarSuffix: "{}",
		"notes.txt":                     "ignored",
		"bin.md":                        "binary that ends in .md",
		"bin.md" + verb.SidecarSuffix:   "{}",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(stories, "not-a-story"), 0o755); err != nil {
		t.Fatal(err)
	}

	ids, err := verb.StoryAttachmentIDs()
	if err != nil || len(ids) != 1 || ids[0] != "sty_aaaa1111" {
		t.Fatalf("ids = %v, %v; want only the story dir", ids, err)
	}
	files, binary, err := verb.StoryAttachmentFiles("sty_aaaa1111")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "plan.md" || string(files[0].Content) != raw {
		t.Errorf("files = %+v, want only plan.md returned raw", files)
	}
	if binary != 2 {
		t.Errorf("binary = %d, want 2 (shot.png and bin.md)", binary)
	}
	if files, binary, err := verb.StoryAttachmentFiles("sty_missing"); err != nil || len(files) != 0 || binary != 0 {
		t.Errorf("a story with no dir = %v, %d, %v; want empty", files, binary, err)
	}

	if err := verb.WriteStoryAttachmentFile("sty_bbbb2222", "change.md", []byte(raw)); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(stories, "sty_bbbb2222", "change.md"))
	if err != nil || string(got) != raw {
		t.Errorf("written file = %q, %v; want the raw bytes", got, err)
	}
	for _, bad := range [][2]string{
		{"sty_bbbb2222", "../escape.md"},
		{"sty_bbbb2222", "sub/dir.md"},
		{"sty_bbbb2222", "shot.png"},
		{"sty_bbbb2222", ""},
		{"../sty_x", "a.md"},
		{"", "a.md"},
	} {
		if err := verb.WriteStoryAttachmentFile(bad[0], bad[1], []byte("x")); err == nil {
			t.Errorf("WriteStoryAttachmentFile(%q, %q) should be refused", bad[0], bad[1])
		}
	}
}
