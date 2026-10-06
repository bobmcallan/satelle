package config

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeWorktreeRepo makes <root>/.satelle/satelle.toml with body and returns its path.
func writeWorktreeRepo(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	dd := filepath.Join(root, DefaultDataDir)
	if err := os.MkdirAll(filepath.Join(dd, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dd, "skills", "x.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dd, ConfigName)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// treeHash digests every file under dir (path and bytes), so a refusal can be
// shown to have rewritten nothing.
func treeHash(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(b))
		h.Write(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// AC2: a malformed [worktree] declaration is refused at Load, naming the entry,
// and nothing under the repo is rewritten.
func TestLoad_RefusesMalformedWorktreeDeclaration(t *testing.T) {
	cases := []struct {
		name, body, entry string
	}{
		{"data dir", `include = [".satelle"]`, ".satelle"},
		{"under data dir", `include = [".satelle/skills"]`, ".satelle/skills"},
		{"custom data dir", "data_dir = \".state\"\n[worktree]\ninclude = [\".state\"]", ".state"},
		{"custom data dir subpath", "data_dir = \".state\"\n[worktree]\ninclude = [\".state/db\"]", ".state/db"},
		{"parent of data dir", `include = ["."]`, "."},
		{"absolute", `include = ["/etc/passwd"]`, "/etc/passwd"},
		{"outside", `include = ["../outside"]`, "../outside"},
		{"escapes after clean", `include = ["a/../../x"]`, "a/../../x"},
		{"git dir", `include = [".git"]`, ".git"},
		{"inside git dir", `include = [".git/hooks"]`, ".git/hooks"},
		{"empty", `include = [""]`, ""},
		{"glob", `include = [".tool*"]`, ".tool*"},
		{"duplicate", `include = [".a", ".a/"]`, ".a/"},
		{"branch without id", `branch = "epic/fixed"`, "epic/fixed"},
		{"path without id", `path = "../trees/fixed"`, "../trees/fixed"},
		{"invalid branch", `branch = "bad branch/{id}"`, "bad branch/{id}"},
		{"branch dotdot", `branch = "a..b/{id}"`, "a..b/{id}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			if !strings.Contains(body, "[worktree]") {
				body = "[worktree]\n" + body
			}
			p := writeWorktreeRepo(t, body)
			root := filepath.Dir(filepath.Dir(p))
			before := treeHash(t, root)

			_, _, err := Load(p)
			if err == nil {
				t.Fatalf("Load accepted a malformed declaration:\n%s", body)
			}
			if !strings.Contains(err.Error(), "[worktree]") || !strings.Contains(err.Error(), fmt.Sprintf("%q", tc.entry)) {
				t.Errorf("error %q does not name the [worktree] entry %q", err, tc.entry)
			}
			if after := treeHash(t, root); after != before {
				t.Errorf("refusing the declaration rewrote files under %s", root)
			}
		})
	}
}

func TestLoad_AcceptsWellFormedWorktreeDeclaration(t *testing.T) {
	p := writeWorktreeRepo(t, "[worktree]\ninclude = [\".env\", \"tools/local\"]\nbranch = \"work/{id}\"\npath = \"../trees/{id}\"\n")
	cfg, _, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := strings.Join(cfg.Worktree.Include, ","); got != ".env,tools/local" {
		t.Errorf("include = %q", got)
	}
	if cfg.Worktree.Branch != "work/{id}" || cfg.Worktree.Path != "../trees/{id}" {
		t.Errorf("templates = %q, %q", cfg.Worktree.Branch, cfg.Worktree.Path)
	}
}

// No [worktree] table declares nothing: the binary ships no default.
func TestLoad_NoWorktreeTableDeclaresNothing(t *testing.T) {
	cfg, _, err := Load(writeWorktreeRepo(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Worktree.Include) != 0 || cfg.Worktree.Branch != "" || cfg.Worktree.Path != "" {
		t.Errorf("an empty config carries a worktree default: %+v", cfg.Worktree)
	}
}
