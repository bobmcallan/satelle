package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// satelle manages no cloud identity (sty_c00782a3): no non-test source under
// internal/ or cmd/ names a cloud provider or its sign-in. The help topics are
// exempt because they are where the retirement is explained.
func TestNoCloudProviderNamesInSource(t *testing.T) {
	root := moduleRoot(t)
	re := regexp.MustCompile(`(?i)\b(azure|aws|gcloud|sts)\b|az login|credential_process`)
	helpTopics := filepath.Join(root, "internal", "help", "topics")
	scanned := 0
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path == helpTopics {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			scanned++
			for i, line := range strings.Split(string(b), "\n") {
				if re.MatchString(line) {
					rel, _ := filepath.Rel(root, path)
					t.Errorf("%s:%d names a cloud provider or sign-in: %s", rel, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d non-test .go files, want at least 100", scanned)
	}
}
