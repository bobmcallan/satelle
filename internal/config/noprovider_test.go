package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// providerWords matches a cloud provider or one of its sign-in mechanisms.
// satelle is a harness: which cloud, which CLI and which sign-in command are the
// operator's configuration, never the binary's (sty_b8310408). The bare word
// "cloud" is not matched — the cloud performer uses it for a harness's own
// hosted sessions.
var providerWords = regexp.MustCompile(`(?i)\b(azure|aws|gcloud|sts)\b|az login|credential_process`)

// TestNoProviderNamesInBinary: no non-test Go source under internal/ or cmd/
// names a provider. The help topics are the one exemption — they carry the
// published Azure and AWS examples, written as configuration only.
func TestNoProviderNamesInBinary(t *testing.T) {
	var scanned int
	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			slash := filepath.ToSlash(path)
			if d.IsDir() {
				if strings.HasSuffix(slash, "internal/help/topics") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			scanned++
			for i, line := range strings.Split(string(b), "\n") {
				if m := providerWords.FindString(line); m != "" {
					t.Errorf("%s:%d names a provider (%q): %s", slash, i+1, m, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d Go files — the walk is not reaching the tree", scanned)
	}
}
