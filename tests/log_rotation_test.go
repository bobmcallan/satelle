//go:build integration

package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestOperationsLogRotates drives the real binary: with a tiny configured size cap,
// the operation log rolls over and retention keeps at most logs_max_files rotated
// files, while the active operations.log stays present and readable so a reviewer's
// grep still works (sty_a67e6e8c).
func TestOperationsLogRotates(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Tiny caps to force rotation within a short run. PREPEND these top-level keys:
	// the scaffold now ends with an active [gate] table (sty_8c3d345c), so appending
	// at EOF would scope them under [gate]. A top-level key must precede every table.
	tomlPath := filepath.Join(repo, ".satelle", "satelle.toml")
	orig, _ := os.ReadFile(tomlPath)
	if err := os.WriteFile(tomlPath, append([]byte("logs_max_size_kb = 1\nlogs_max_files = 2\n"), orig...), 0o644); err != nil {
		t.Fatal(err)
	}

	// Enough mutations to grow operations.log well past the 1 KiB cap several times.
	for i := 0; i < 40; i++ {
		mustRun(t, testBin, repo, "story", "create",
			"--title", fmt.Sprintf("rotation padding story %d xxxxxxxxxx", i),
			"--body", "b", "--acceptance", "1. x")
	}

	logsDir := filepath.Join(runtimeRoot(t, repo), "logs")
	if _, err := os.Stat(filepath.Join(logsDir, "operations.log")); err != nil {
		t.Fatalf("active operations.log missing after rotation: %v", err)
	}
	rot, _ := filepath.Glob(filepath.Join(logsDir, "operations-*.log"))
	if len(rot) == 0 {
		t.Fatal("expected the operation log to have rotated, but found no rotated files")
	}
	if len(rot) > 2 {
		t.Errorf("retention not enforced: expected <= 2 rotated files, got %d: %v", len(rot), rot)
	}
}
