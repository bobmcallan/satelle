package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRepoGitignoreCarriesTheGeneratedManagedBlock (sty_097833e8 AC9): this repo's
// committed .gitignore holds exactly the managed block the current init
// generates for it, so running `satelle init` here leaves no .gitignore diff.
func TestRepoGitignoreCarriesTheGeneratedManagedBlock(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".gitignore"))
	if err != nil {
		t.Skipf("no repo .gitignore beside the sources: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, gitignoreMarker) {
		t.Skip("repo .gitignore has no satelle managed block")
	}
	want := gitignoreBlockFor(text)
	if !strings.Contains(text, want) {
		t.Errorf("the committed .gitignore managed block differs from what init generates; want:\n%s", want)
	}
	if !strings.Contains(want, ".satelle/workflows/agents.local.toml") {
		t.Error("the generated block no longer ignores the machine-local agents file")
	}
}
