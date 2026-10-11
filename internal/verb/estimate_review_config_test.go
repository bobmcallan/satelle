package verb_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoEstimateToleranceConstantInGo pins that the estimate gate's tolerance
// and minimum comparable count are repo configuration, never a number in Go:
// no non-test Go source names the [estimate_review] table or its keys, or
// declares a const/var for an estimate tolerance or minimum. The rule, its
// defaults and its config read live in the satelle-estimate-actual-review
// skill's check fence.
func TestNoEstimateToleranceConstantInGo(t *testing.T) {
	ident := regexp.MustCompile(`(?i)\b(const|var)\s+\w*estimate\w*(tolerance|mincomparable|mincomparables|mincount)\w*`)
	literals := []string{"estimate_review", "min_comparables"}
	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			src := string(b)
			for _, lit := range literals {
				if strings.Contains(src, lit) {
					t.Errorf("%s names %q — the estimate gate's tolerance/minimum are repo config read by the skill fence, not Go", path, lit)
				}
			}
			if m := ident.FindString(src); m != "" {
				t.Errorf("%s declares %q — an estimate tolerance/minimum must not be a Go constant", path, m)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
