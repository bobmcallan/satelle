//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isReleaseTestBin reports whether testBin carries a non-dev version so
// refuseBreakingDrift / writeDeployedVersion are live.
func isReleaseTestBin(t *testing.T) bool {
	t.Helper()
	verOut := mustRun(t, testBin, t.TempDir(), "version")
	// satelle 0.0.235 (...) — skip 0.0.0-dev+… and bare "dev"
	if strings.Contains(verOut, "0.0.0-dev") || strings.Contains(verOut, "-dev+") {
		return false
	}
	if strings.Contains(verOut, "satelle dev ") || strings.HasPrefix(strings.TrimSpace(verOut), "satelle dev") {
		return false
	}
	return true
}

// TestInitHealsUnstampedIdentical (sty_a9ec33e7 AC4): a pre-stamp-shaped repo
// (embedded files without embedded_sha, no deployed.version) is healed by one
// satelle init — validation passes and a store-backed verb works afterward.
func TestInitHealsUnstampedIdentical(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	materializeDefault(t, repo, "skills", "satelle-step-summary")
	data := filepath.Join(repo, ".satelle")
	_ = os.Remove(filepath.Join(data, "deployed.version"))

	// Strip embedded_sha from the materialised skill.
	stripped := filepath.Join(data, "skills", "satelle-step-summary.md")
	b0, err := os.ReadFile(stripped)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b0), "\n")
	var outLines []string
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "embedded_sha:") {
			continue
		}
		outLines = append(outLines, ln)
	}
	if err := os.WriteFile(stripped, []byte(strings.Join(outLines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	out := mustRun(t, testBin, repo, "init")
	if !strings.Contains(out, "restamped") {
		t.Fatalf("init output must report restamped for identical stampless body:\n%s", out)
	}
	// stamp restored on stripped file
	b, err := os.ReadFile(stripped)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "embedded_sha:") {
		t.Fatalf("file was not restamped: %s", stripped)
	}
	// store-backed verb works after heal
	if _, err := run(t, testBin, repo, "status"); err != nil {
		t.Fatalf("status after heal: %v", err)
	}
	// On a release binary, deployed.version must be written by successful init.
	if isReleaseTestBin(t) {
		if _, err := os.Stat(filepath.Join(data, "deployed.version")); err != nil {
			t.Fatalf("deployed.version missing after heal init: %v", err)
		}
	}
}

// TestRestoreExemptFromDriftGate (sty_a9ec33e7 AC2): restore runs when
// deployed.version is missing (heal path), while other store-backed verbs refuse.
// Requires a release-versioned binary (make integration); skipped for bare `go test` dev builds.
func TestRestoreExemptFromDriftGate(t *testing.T) {
	if !isReleaseTestBin(t) {
		t.Skip("dev binary never gates refuseBreakingDrift")
	}
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	data := filepath.Join(repo, ".satelle")
	skillDir := filepath.Join(data, "skills")
	ents, _ := os.ReadDir(skillDir)
	var skillPath string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			skillPath = filepath.Join(skillDir, e.Name())
			break
		}
	}
	if skillPath == "" {
		t.Fatal("no skill to drift")
	}
	if err := os.WriteFile(skillPath, []byte("drifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(data, "deployed.version"))

	// Other store-backed verbs refuse.
	if out, err := run(t, testBin, repo, "status"); err == nil {
		t.Fatalf("status should refuse without deployed.version:\n%s", out)
	}

	// restore --yes must succeed (exempt).
	out, err := run(t, testBin, repo, "restore", "--yes")
	if err != nil {
		t.Fatalf("restore --yes should heal without stamp gate: %v\n%s", err, out)
	}
	// After restore, deployed.version must exist on a release binary.
	if _, err := os.Stat(filepath.Join(data, "deployed.version")); err != nil {
		t.Fatalf("deployed.version missing after restore: %v", err)
	}
}
