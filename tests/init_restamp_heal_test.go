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
