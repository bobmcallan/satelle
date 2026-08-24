package tests

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInternalGoHasNoFable pins sty_bdeff052 AC3: the binary ships no
// product-family name. Test files are excluded.
func TestInternalGoHasNoFable(t *testing.T) {
	root := filepath.Join("..", "internal")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(strings.ToLower(string(b)), "fable") {
			t.Errorf("%s contains %q", path, "fable")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
