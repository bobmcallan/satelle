//go:build integration

package tests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// readVersionField reads key from the repo's .version (module root = parent of
// tests/). Usable from TestMain (no *testing.T). Keys include "satelle.version:",
// "satelle.build:", "satelled.version:".
func readVersionField(key string) (string, error) {
	b, err := os.ReadFile(filepath.Join("..", ".version"))
	if err != nil {
		return "", err
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if f := strings.Fields(ln); len(f) == 2 && f[0] == key {
			return f[1], nil
		}
	}
	return "", fmt.Errorf(".version has no %s line", key)
}

// repoVersion reads the canonical satelle.version value from the repo's .version
// (the single source of truth, sty_27077b11).
func repoVersion(t *testing.T) string {
	t.Helper()
	v, err := readVersionField("satelle.version:")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// repoBuildDate reads the canonical satelle.build value from the repo's .version —
// the single source for the build date, stamped by the commit step (sty_3aeeab18).
func repoBuildDate(t *testing.T) string {
	t.Helper()
	v, err := readVersionField("satelle.build:")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// repoServeVersion reads satelled.version from .version (sty_19ff03f4).
func repoServeVersion(t *testing.T) string {
	t.Helper()
	v, err := readVersionField("satelled.version:")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// releaseTestBinLdflags returns the same -ldflags shape `make build` uses for
// cmd/satelle, so TestMain's testBin is a release-stamped binary.
// writeDeployedVersion refuses to stamp a dev sentinel; without these flags the
// deployed.version and scaffold-drift tests can never pass (sty_4c986ed8).
//
// Commit is a fixed short hex so TestMain has no git dependency (tarball-safe).
func releaseTestBinLdflags() (string, error) {
	ver, err := readVersionField("satelle.version:")
	if err != nil {
		return "", err
	}
	build, err := readVersionField("satelle.build:")
	if err != nil {
		return "", err
	}
	const pkg = "github.com/bobmcallan/satelle/internal/buildinfo"
	const commit = "testbin000001"
	return fmt.Sprintf(
		"-X %s.Name=satelle -X %s.Version=%s -X %s.Commit=%s -X %s.BuildTime=%s",
		pkg, pkg, ver, pkg, commit, pkg, build,
	), nil
}

// TestMakefileStampsPerArtifactVersions proves `make build` stamps each main
// with its own version from .version (sty_19ff03f4 / sty_3aeeab18).
func TestMakefileStampsPerArtifactVersions(t *testing.T) {
	cliVer := repoVersion(t)
	serveVer := repoServeVersion(t)
	build := repoBuildDate(t)
	cmd := exec.Command("make", "-n", "build")
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n build: %v\n%s", err, out)
	}
	s := string(out)
	// Split by go build lines so a shared-LDFLAGS regression fails.
	var cliLine, serveLine string
	for _, ln := range strings.Split(s, "\n") {
		if strings.Contains(ln, "./cmd/satelled") {
			serveLine = ln
		} else if strings.Contains(ln, "./cmd/satelle") {
			cliLine = ln
		}
	}
	if cliLine == "" || serveLine == "" {
		t.Fatalf("make -n build missing mains:\n%s", s)
	}
	if !strings.Contains(cliLine, "Version="+cliVer) {
		t.Errorf("CLI line must stamp Version=%s:\n%s", cliVer, cliLine)
	}
	if !strings.Contains(serveLine, "Version="+serveVer) {
		t.Errorf("serve line must stamp Version=%s:\n%s", serveVer, serveLine)
	}
	if cliVer != serveVer {
		// Exact Version=V token only — "Version=0.0.2" must not match "Version=0.0.290".
		exact := func(line, ver string) bool {
			needle := ".Version=" + ver
			i := strings.Index(line, needle)
			if i < 0 {
				return false
			}
			end := i + len(needle)
			if end == len(line) {
				return true
			}
			c := line[end]
			return c == ' ' || c == '"' || c == '\''
		}
		if exact(cliLine, serveVer) {
			t.Errorf("CLI line must not use serve version %s:\n%s", serveVer, cliLine)
		}
		if exact(serveLine, cliVer) {
			t.Errorf("serve line must not use CLI version %s:\n%s", cliVer, serveLine)
		}
	}
	if !strings.Contains(s, "BuildTime="+build) {
		t.Errorf("make build must bake BuildTime=%s:\n%s", build, s)
	}
}

// TestBuildDateBakedFromVersionFile proves end-to-end that a build using the date
// from .version reports that exact date through `satelle version` — so .version is
// the single source for the build identity (version + date).
func TestBuildDateBakedFromVersionFile(t *testing.T) {
	ver := repoVersion(t)
	build := repoBuildDate(t)
	pkg := "github.com/bobmcallan/satelle/internal/buildinfo"
	ldflags := "-X " + pkg + ".Version=" + ver +
		" -X " + pkg + ".Commit=abc123def456" +
		" -X " + pkg + ".BuildTime=" + build
	bin := filepath.Join(t.TempDir(), "satelle-dated")
	b := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, "./cmd/satelle")
	b.Dir = ".."
	if out, err := b.CombinedOutput(); err != nil {
		t.Fatalf("ldflags build: %v\n%s", err, out)
	}
	// Run outside the source tree so a repo-local .satelle/satelle pin is not
	// re-exec'd in place of the stamped binary under test.
	cmd := exec.Command(bin, "version")
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("satelle version: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "built "+build) {
		t.Errorf("version output must report the .version build date %q:\n%s", build, out)
	}
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

	// Isolated cwd: avoid re-exec into this repo's .satelle/satelle pin.
	runDir := t.TempDir()
	for _, args := range [][]string{{"version"}, {"--version"}} {
		cmd := exec.Command(bin, args...)
		cmd.Dir = runDir
		out, err := cmd.CombinedOutput()
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
