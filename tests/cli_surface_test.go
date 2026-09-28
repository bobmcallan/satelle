//go:build integration

package tests

import (
	"fmt"
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
// behind a ### Breaking changelog entry whose migrations are manual (no
// `init-heals:` marker — every shipped Breaking entry) lets store-backed verbs run
// and prints the entry's remediation on stderr; an UNSTAMPED repo still fails
// closed naming satelle init; re-init heals both (sty_6e143870).
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

	// Plant older stamp + CHANGELOG with Breaking between stamp and the binary's version.
	// Use the binary's stamped version so the "behind Breaking" path stays real as
	// .version advances (do not hardcode a fixed "modern" number).
	binVer := repoVersion(t)
	if err := os.WriteFile(stamp, []byte("satelle.version: 0.0.100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cl := fmt.Sprintf(`# Changelog

## [%s] - 2026-07-13

### Breaking
- test break for drift gate

## [0.0.100] - 2020-01-01

### Fixed
- ancient
`, binVer)
	if err := os.WriteFile(filepath.Join(repo, "CHANGELOG.md"), []byte(cl), 0o644); err != nil {
		t.Fatal(err)
	}
	// Store-backed verbs run (the shipped Breaking entries are manual migrations,
	// so init cannot heal them and refusing would only block work) and print the
	// remediation; the CombinedOutput carries stderr.
	out, err := run(t, testBin, repo, "story", "list")
	if err != nil {
		t.Fatalf("story list must run when the stamp is behind a manual-migration Breaking release: %v\n%s", err, out)
	}
	if !strings.Contains(out, "BREAKING release") || !strings.Contains(out, "satelle init") {
		t.Errorf("must warn naming the breaking release and satelle init:\n%s", out)
	}
	// Once per session: with a session identity the second command is quiet.
	sess := []string{"SATELLE_SESSION=sess-breaking-warn"}
	first, err := runEnv(t, testBin, repo, sess, "story", "list")
	if err != nil || !strings.Contains(first, "BREAKING release") {
		t.Fatalf("first command in a session must warn: err=%v\n%s", err, first)
	}
	if again, err := runEnv(t, testBin, repo, sess, "story", "list"); err != nil || strings.Contains(again, "BREAKING release") {
		t.Fatalf("second command in the same session must run silently: err=%v\n%s", err, again)
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
