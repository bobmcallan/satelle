package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAttachBody covers `story attach`'s body resolution (sty_97c53d72): --file
// reads the body from a file, --body passes through, and a missing file is a
// clear error (the --body/--file conflict is enforced by cobra's
// MarkFlagsMutuallyExclusive on the command).
func TestAttachBody(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "summary.md")
	if err := os.WriteFile(p, []byte("# from file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, err := attachBody("inline", ""); err != nil || got != "inline" {
		t.Errorf("body passthrough = (%q, %v), want (inline, nil)", got, err)
	}
	if got, err := attachBody("", p); err != nil || got != "# from file\n" {
		t.Errorf("file read = (%q, %v), want file content", got, err)
	}
	if _, err := attachBody("", filepath.Join(dir, "absent.md")); err == nil || !strings.Contains(err.Error(), "read --file") {
		t.Errorf("missing file should error with path context, got %v", err)
	}
}
