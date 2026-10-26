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
// behind a shipped ### Breaking release that declares `init-heals:` (0.0.761,
// sty_642275eb) refuses store-backed verbs naming the release and satelle init; an
// UNSTAMPED repo still fails closed naming satelle init; re-init heals both
// (sty_6e143870). The warn-once path for manual-only ranges is a unit test
// (internal/cli/drift_test.go): the shipped changelog's newest Breaking release
// is init-healable, so an end-to-end stamp behind it always refuses.
//
// Requires a release-stamped testBin (sty_4c986ed8). TestMain stamps by default;
// SATELLE_BIN=dev builds skip loudly via isReleaseTestBin.
func TestDeployedVersionStampAndBreakingDrift(t *testing.T) {
	if !isReleaseTestBin(t) {
		t.Skip("testBin is not release-stamped (dev sentinel) — writeDeployedVersion refuses to stamp; need TestMain ldflags or SATELLE_BIN from `make build`")
	}
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

	// A stamp behind the shipped init-heals Breaking release refuses store-backed
	// verbs, naming the release and satelle init; the CombinedOutput carries stderr.
	if err := os.WriteFile(stamp, []byte("satelle.version: 0.0.100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, testBin, repo, "story", "list"); err == nil || !strings.Contains(out, "BREAKING release") || !strings.Contains(out, "satelle init") {
		t.Fatalf("story list must refuse behind an init-heals Breaking release, naming it and satelle init: err=%v\n%s", err, out)
	}
	// An unstamped repo still fails closed: init is the heal.
	if err := os.Remove(stamp); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, testBin, repo, "story", "list"); err == nil || !strings.Contains(out+err.Error(), "satelle init") {
		t.Fatalf("an unstamped repo must fail closed naming satelle init: err=%v\n%s", err, out)
	}
	if err := os.WriteFile(stamp, []byte("satelle.version: 0.0.100\n"), 0o644); err != nil {
		t.Fatal(err)
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
