package verb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// substratePatch renders gitignored files as whole-file adds through the shared
// unified-diff renderer (sty_98a2816b AC2).
func TestSubstratePatch(t *testing.T) {
	dir := t.TempDir()
	put := func(name string, body []byte) substrateFile {
		t.Helper()
		abs := filepath.Join(dir, name)
		if err := os.WriteFile(abs, body, 0o644); err != nil {
			t.Fatal(err)
		}
		return substrateFile{Rel: ".satelle/skills/" + name, Abs: abs}
	}

	cases := []struct {
		name    string
		added   []substrateFile
		deleted []string
		labels  []string
		want    []string
		absent  []string
	}{
		{
			name:  "text is a whole-file add",
			added: []substrateFile{put("t.md", []byte("one\ntwo\n"))},
			want: []string{
				"# substrate (untracked; whole file shown)\n",
				"diff --git a/.satelle/skills/t.md b/.satelle/skills/t.md\n",
				"--- /dev/null\n+++ b/.satelle/skills/t.md\n",
				"@@ -0,0 +1,2 @@\n+one\n+two\n",
			},
		},
		{
			name:  "empty file has headers and no hunk",
			added: []substrateFile{put("e.md", nil)},
			want:  []string{"+++ b/.satelle/skills/e.md\n"},
			absent: []string{
				"@@",
			},
		},
		{
			name:   "binary is named, not dumped",
			added:  []substrateFile{put("b.bin", []byte("ab\x00cd"))},
			want:   []string{"Binary file .satelle/skills/b.bin changed\n"},
			absent: []string{"+ab", "--- /dev/null"},
		},
		{
			name:    "deleted path is a header only",
			deleted: []string{".satelle/skills/old.md"},
			want:    []string{"deleted file mode 100644\ndeleted file .satelle/skills/old.md\n"},
			absent:  []string{"--- /dev/null"},
		},
		{
			name:   "labels follow the header",
			added:  []substrateFile{put("l.md", []byte("x\n"))},
			labels: []string{"shared-attribution: also in flight sty_1"},
			want:   []string{"# substrate (untracked; whole file shown)\n# shared-attribution: also in flight sty_1\n"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := substratePatch(tc.added, tc.deleted, tc.labels)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(got, a) {
					t.Errorf("unexpected %q in:\n%s", a, got)
				}
			}
		})
	}

	if got := substratePatch(nil, nil, []string{"label"}); got != "" {
		t.Errorf("nothing to render must yield no section, got %q", got)
	}

	t.Run("oversized content is cut with a marker", func(t *testing.T) {
		old := changeRecordPatchLimit
		changeRecordPatchLimit = 512
		t.Cleanup(func() { changeRecordPatchLimit = old })
		f := put("big.md", []byte(strings.Repeat("a line of text\n", 400)))
		got := substratePatch([]substrateFile{f}, nil, nil)
		if !strings.Contains(got, "[truncated at 512 bytes]") {
			t.Errorf("oversized section must carry a cut marker:\n%s", got)
		}
		if len(got) > 512+64 {
			t.Errorf("section is %d bytes, want it bounded near the 512 limit", len(got))
		}
	})
}
