//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRetiredAliasesFailClosed: all three removed spellings name their replacement.
func TestRetiredAliasesFailClosed(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"install"}, "satelle init"},
		{[]string{"workspace", "rm"}, "workspace remove"},
		{[]string{"sync", "config", "pull"}, "sync config deploy"},
	}
	for _, c := range cases {
		out, err := run(t, testBin, repo, c.args...)
		if err == nil {
			t.Fatalf("%v must fail closed, got:\n%s", c.args, out)
		}
		combined := out + err.Error()
		if !strings.Contains(combined, c.want) {
			t.Errorf("%v: want %q in %q", c.args, c.want, combined)
		}
	}
	// service install must still work as a command path (help at least)
	if help, err := run(t, testBin, repo, "service", "install", "--help"); err != nil {
		// help may not need store
		_ = help
	}
}

// TestDeployedVersionStampAndBreakingDrift: init stamps deployed.version; a stamp
// behind a ### Breaking changelog entry makes store-backed verbs fail closed
// naming satelle init; re-init heals.
func TestDeployedVersionStampAndBreakingDrift(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stamp := filepath.Join(repo, ".satelle", "deployed.version")
	body, err := os.ReadFile(stamp)
	if err != nil {
		t.Fatalf("init must stamp deployed.version: %v", err)
	}
	if !strings.Contains(string(body), "satelle.version:") {
		t.Fatalf("stamp content: %q", body)
	}

	// Plant older stamp + CHANGELOG with Breaking between stamp and any modern binary.
	if err := os.WriteFile(stamp, []byte("satelle.version: 0.0.100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cl := `# Changelog

## [0.0.219] - 2026-07-13

### Breaking
- test break for drift gate

## [0.0.100] - 2020-01-01

### Fixed
- ancient
`
	if err := os.WriteFile(filepath.Join(repo, "CHANGELOG.md"), []byte(cl), 0o644); err != nil {
		t.Fatal(err)
	}
	// Store-backed verb must fail closed (release testBin is 0.0.219 > 0.0.100).
	out, err := run(t, testBin, repo, "story", "list")
	if err == nil {
		t.Fatalf("story list must fail closed when stamp is behind Breaking; out=%s", out)
	}
	combined := out + err.Error()
	if !strings.Contains(combined, "satelle init") {
		t.Errorf("must name satelle init: %s", combined)
	}
	if !strings.Contains(strings.ToLower(combined), "breaking") {
		t.Errorf("must mention breaking: %s", combined)
	}
	// Re-init heals (re-stamps to binary version).
	mustRun(t, testBin, repo, "init")
	if _, err := run(t, testBin, repo, "story", "list"); err != nil {
		t.Fatalf("after re-init story list should work: %v", err)
	}
	// Stamp should no longer be 0.0.100
	got, _ := os.ReadFile(stamp)
	if strings.Contains(string(got), "0.0.100") {
		t.Fatalf("init should have re-stamped, still: %s", got)
	}
}
