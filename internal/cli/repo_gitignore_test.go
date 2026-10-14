package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRepoGitignoreNeedsNoConverge (sty_dbc0be85 AC7): this repo's committed
// .gitignore already settles everything satelle needs, so running `satelle
// init` here leaves no .gitignore diff.
func TestRepoGitignoreNeedsNoConverge(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Skipf("no repo .gitignore beside the sources: %v", err)
	}
	if _, added, changed := convergeGitignore(string(raw), root); changed {
		t.Errorf("the committed .gitignore lacks entries satelle needs: %v", added)
	}
}
