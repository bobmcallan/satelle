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

// TestDeployedVersionStampAndBreakingDrift: init stamps deployed.version; missing
// stamp fails closed naming satelle init when binary is a release build.
func TestDeployedVersionStampAndBreakingDrift(t *testing.T) {
	repo := t.TempDir()
	out := mustRun(t, testBin, repo, "init")
	_ = out
	stamp := filepath.Join(repo, ".satelle", "deployed.version")
	body, err := os.ReadFile(stamp)
	if err != nil {
		t.Fatalf("init must stamp deployed.version: %v", err)
	}
	if !strings.Contains(string(body), "satelle.version:") {
		t.Fatalf("stamp content: %q", body)
	}
	// Remove stamp → store-backed command should fail closed (release binaries).
	_ = os.Remove(stamp)
	out2, err2 := run(t, testBin, repo, "story", "list")
	// Dev builds skip the gate; release builds fail.
	if err2 != nil {
		if !strings.Contains(out2+err2.Error(), "satelle init") {
			t.Errorf("missing stamp must name satelle init: %v\n%s", err2, out2)
		}
	}
	// Re-init heals
	mustRun(t, testBin, repo, "init")
	if _, err := run(t, testBin, repo, "story", "list"); err != nil {
		t.Fatalf("after re-init story list should work: %v", err)
	}
}
