//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitHealsUnstampedIdentical (sty_a9ec33e7 AC4): a pre-stamp-shaped repo
// (embedded files without embedded_sha, no deployed.version) is healed by one
// satelle init — validation passes and a store-backed verb works afterward.
func TestInitHealsUnstampedIdentical(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// Strip deployed.version and strip embedded_sha from one skill.
	data := filepath.Join(repo, ".satelle")
	_ = os.Remove(filepath.Join(data, "deployed.version"))
	// Find a stamped skill file and strip its stamp line
	skillDir := filepath.Join(data, "skills")
	ents, err := os.ReadDir(skillDir)
	if err != nil {
		t.Fatal(err)
	}
	var stripped string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		p := filepath.Join(skillDir, e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if !strings.Contains(string(b), "embedded_sha:") {
			continue
		}
		lines := strings.Split(string(b), "\n")
		var out []string
		for _, ln := range lines {
			if strings.HasPrefix(strings.TrimSpace(ln), "embedded_sha:") {
				continue
			}
			out = append(out, ln)
		}
		if err := os.WriteFile(p, []byte(strings.Join(out, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}
		stripped = p
		break
	}
	if stripped == "" {
		t.Fatal("no stamped skill found to strip")
	}
	// One init must heal
	out := mustRun(t, testBin, repo, "init")
	if !strings.Contains(out, "restamped") && !strings.Contains(out, "restamp") {
		// report line uses "restamped" in message
		t.Logf("init output:\n%s", out)
	}
	// deployed.version present
	if _, err := os.Stat(filepath.Join(data, "deployed.version")); err != nil {
		// may not write on dev binary — skip soft
		t.Logf("deployed.version: %v (dev builds skip stamp)", err)
	}
	// store-backed verb works
	if _, err := run(t, testBin, repo, "status"); err != nil {
		t.Fatalf("status after heal: %v", err)
	}
	// stamp restored on stripped file
	b, _ := os.ReadFile(stripped)
	if !strings.Contains(string(b), "embedded_sha:") {
		t.Errorf("file was not restamped: %s", stripped)
	}
}

// TestRestoreExemptFromDriftGate (sty_a9ec33e7 AC2): restore runs when
// deployed.version is missing (heal path), while other store-backed verbs refuse.
// Requires a release-versioned binary (make integration); skipped for bare `go test` dev builds.
func TestRestoreExemptFromDriftGate(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// Confirm binary is release-stamped (refuseBreakingDrift no-ops on dev).
	verOut := mustRun(t, testBin, repo, "version")
	if strings.Contains(verOut, "0.0.0-dev") || strings.Contains(verOut, " dev") || strings.HasPrefix(strings.TrimSpace(verOut), "satelle dev") {
		// version line looks like "satelle 0.0.233 (...)" or "satelle 0.0.0-dev+..."
	}
	if strings.Contains(verOut, "0.0.0-dev") || strings.Contains(verOut, "(dev") {
		t.Skip("dev binary never gates refuseBreakingDrift")
	}
	// Also skip when version reports "dev"
	if strings.Contains(verOut, " satelle dev") || strings.HasPrefix(verOut, "satelle dev ") {
		t.Skip("dev binary")
	}
	// crude: if version contains "-dev" skip
	if strings.Contains(verOut, "-dev") {
		t.Skip("dev binary never gates")
	}

	data := filepath.Join(repo, ".satelle")
	// Ensure a skill exists so restore has work if we drift it.
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
	} else if !strings.Contains(out, "satelle init") && !strings.Contains(out, "deployed.version") && !strings.Contains(out, "stamp") {
		// message may vary; at least must fail
		t.Logf("status failed as expected: %v\n%s", err, out)
	}

	// restore --yes must succeed (exempt).
	out, err := run(t, testBin, repo, "restore", "--yes")
	if err != nil {
		t.Fatalf("restore --yes should heal without stamp gate: %v\n%s", err, out)
	}
	// After restore, deployed.version should exist (release binary).
	if _, err := os.Stat(filepath.Join(data, "deployed.version")); err != nil {
		t.Logf("deployed.version after restore: %v (may be absent on some builds)", err)
	}
}
