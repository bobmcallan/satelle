package remotedef

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// denied is the set of names the engine must never carry: a host, vendor, cloud,
// identity provider, repo or model. Matched on word boundaries so an ordinary
// word that merely contains one (draws, flying) does not trip it.
// It also names the event-source and CI vocabulary, so the trigger and wake
// source stay neutral about what fires a remote.
var denied = regexp.MustCompile(`(?i)\b(fly|sprite|azure|aws|github|teleport|claude|anthropic|openai|gitlab|bitbucket|gitea|jenkins|circleci|travis|buildkite|webhook|cron)\b`)

// TestEngineNamesNoHostOrVendor scans the package's non-test source — code,
// comments and the scaffold's placeholders alike — for a vendor. The definition
// carries those as opaque strings its author owns; the engine holds no list.
func TestEngineNamesNoHostOrVendor(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source found: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		inImports := false
		for i, line := range strings.Split(string(b), "\n") {
			// An import path is a module path, not a name the engine carries.
			switch {
			case strings.HasPrefix(line, "import ("):
				inImports = true
				continue
			case inImports && line == ")":
				inImports = false
				continue
			case inImports:
				continue
			}
			if m := denied.FindString(line); m != "" {
				t.Errorf("%s:%d names %q: %s", f, i+1, m, strings.TrimSpace(line))
			}
		}
	}
}

func TestDeniedMatchesOnWordBoundaries(t *testing.T) {
	for _, ok := range []string{"draws", "flying", "spritely", "a lawsuit", "macron", "cronies", "webhooks-free", "giteam", "travisty"} {
		if denied.MatchString(ok) {
			t.Errorf("%q must not match", ok)
		}
	}
	for _, bad := range []string{"AWS", "run on aws.", "(Fly)", "github-hosted", "Claude", "GitLab", "bitbucket pipelines", "a webhook", "(cron)", "Gitea", "Jenkins", "circleci", "Travis", "buildkite"} {
		if !denied.MatchString(bad) {
			t.Errorf("%q must match", bad)
		}
	}
}
