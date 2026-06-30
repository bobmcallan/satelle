//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoVersion reads the canonical satelle.version value from the repo's .version
// (the single source of truth, sty_27077b11).
func repoVersion(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", ".version"))
	if err != nil {
		t.Fatalf("read .version: %v", err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if f := strings.Fields(ln); len(f) == 2 && f[0] == "satelle.version:" {
			return f[1]
		}
	}
	t.Fatal(".version has no satelle.version: line")
	return ""
}

// TestVersionBakedFromLdflags proves the flagged build (sty_27077b11): building
// cmd/satelle with the same -ldflags `make build` uses bakes the canonical version
// from .version, a real commit, and a build timestamp — so a clean build reports a
// correct, non-empty version through both `version` and `--version`, never the
// bare "dev" sentinel.
func TestVersionBakedFromLdflags(t *testing.T) {
	ver := repoVersion(t)
	if ver == "" {
		t.Fatal("empty version")
	}
	pkg := "github.com/bobmcallan/satelle/internal/buildinfo"
	ldflags := "-X " + pkg + ".Version=" + ver +
		" -X " + pkg + ".Commit=abc123def456" +
		" -X " + pkg + ".BuildTime=2026-01-02T03:04:05Z"

	bin := filepath.Join(t.TempDir(), "satelle-stamped")
	build := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, "./cmd/satelle")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("ldflags build: %v\n%s", err, out)
	}

	for _, args := range [][]string{{"version"}, {"--version"}} {
		out, err := exec.Command(bin, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("satelle %v: %v\n%s", args, err, out)
		}
		s := string(out)
		for _, want := range []string{"satelle " + ver, "commit abc123def456", "built 2026-01-02T03:04:05Z"} {
			if !strings.Contains(s, want) {
				t.Errorf("satelle %v output missing %q:\n%s", args, want, s)
			}
		}
		if strings.Contains(s, "0.0.0-dev") || strings.Contains(s, "commit none") {
			t.Errorf("stamped build must not report the dev/none sentinel:\n%s", s)
		}
	}
}
