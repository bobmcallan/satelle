package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The scope reviewer's tidy hint is authored in the skill, not compiled in
// (sty_d74e9b1b AC4): the embedded skill carries the wording, and no non-test
// Go file does.
func TestScopeReviewSkillCarriesTidyHint(t *testing.T) {
	var body string
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == "satelle-story-scope-review" {
			body = d.Body
		}
	}
	if body == "" {
		t.Fatal("embedded satelle-story-scope-review skill not found")
	}
	for _, want := range []string{"Fix: satelle story tidy <story.id> <path1> <path2>", "untracked"} {
		if !strings.Contains(body, want) {
			t.Errorf("scope review skill missing %q", want)
		}
	}
}

func TestNoTidyHintWordingInGo(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), "Fix: satelle story tidy") {
			t.Errorf("%s compiles the tidy hint wording into Go; it belongs in the reviewer skill", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
