//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVersionDriftAdvisoryAtSessionStart (sty_8ecdae90) drives the REAL binary
// through `satelle hook context` to prove the version-stamp advisory reaches the
// SessionStart body — the half a unit test cannot reach, because
// versionDriftAdvisory short-circuits on isDevVersion and the internal/cli test
// binary is a dev build, so every in-package assertion observes silence.
//
// Requires a release-stamped testBin (TestMain stamps by default; SATELLE_BIN
// from a dev build skips loudly, matching the sibling drift tests).
func TestVersionDriftAdvisoryAtSessionStart(t *testing.T) {
	if !isReleaseTestBin(t) || testBinIsUnreleasedBuild(t) {
		t.Skip("testBin is not release-stamped — the advisory short-circuits on dev builds (isDevVersion); use TestMain's stamped build (`make integration`), not a SATELLE_BIN from `make build`")
	}
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stamp := filepath.Join(repo, ".satelle", "deployed.version")
	binVer := repoVersion(t)

	// (b) AC2 — init just stamped this repo at the binary's own version, so the
	// common path must inject NOTHING. A line per session for nothing is a token
	// toll on every session in every repo. Asserted FIRST so the behind-case
	// below cannot pass by emitting unconditionally.
	if ctx := mustRun(t, testBin, repo, "hook", "context"); versionAdvisoryLine(ctx) != "" {
		t.Errorf("a repo stamped at the binary's version must inject no version line, got:\n%s", versionAdvisoryLine(ctx))
	}

	// (a) AC1 — stamped behind: ONE line naming the binary, this repo's stamp,
	// the breaking classification, and the heal command.
	if err := os.WriteFile(stamp, []byte("satelle.version: 0.0.100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	line := versionAdvisoryLine(mustRun(t, testBin, repo, "hook", "context"))
	if line == "" {
		t.Fatal("a repo stamped behind the binary must inject a version-drift advisory")
	}
	for _, want := range []string{binVer, "0.0.100", "satelle init"} {
		if !strings.Contains(line, want) {
			t.Errorf("advisory missing %q:\n%s", want, line)
		}
	}
	// Breaking-or-not is stated either way — the operator must never have to
	// guess whether the gap is the dangerous kind.
	if !strings.Contains(line, "BREAKING release ") && !strings.Contains(line, "no breaking release in that range") {
		t.Errorf("advisory must classify the range as breaking or not:\n%s", line)
	}
	// AC3: advisory, never a refusal — `hook context` still exits zero and still
	// delivers the session body to a stale repo (mustRun would have failed the
	// test on a non-zero exit; assert the content arrived too).
	if ctx := mustRun(t, testBin, repo, "hook", "context"); !strings.Contains(ctx, "additionalContext") {
		t.Errorf("a stale repo must still receive its session context:\n%s", ctx)
	}

	// AC1 — an UNSTAMPED repo is the case the first store-backed verb refuses
	// outright, so it is exactly the one worth warning about ahead of time.
	if err := os.Remove(stamp); err != nil {
		t.Fatal(err)
	}
	unstamped := versionAdvisoryLine(mustRun(t, testBin, repo, "hook", "context"))
	if !strings.Contains(unstamped, "no .satelle/deployed.version stamp") {
		t.Errorf("an unstamped repo must be warned before the refusal:\n%s", unstamped)
	}

	// (c) AC4 — scaffold drift with a MATCHING stamp: the scaffold block appears
	// exactly as it does today and NO version line joins it. The two surfaces
	// have different triggers and neither replaces the other.
	if err := os.WriteFile(stamp, []byte("satelle.version: "+binVer+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hookScript := filepath.Join(repo, ".satelle", "hooks", "satelle-hook.sh")
	if err := os.WriteFile(hookScript, []byte("#!/bin/sh\n# drifted\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := mustRun(t, testBin, repo, "hook", "context")
	if !strings.Contains(ctx, "scaffolding drifts") {
		t.Errorf("scaffold-drift block must be unchanged and still fire:\n%s", ctx)
	}
	if l := versionAdvisoryLine(ctx); l != "" {
		t.Errorf("scaffold drift alone must not inject a version line, got:\n%s", l)
	}

	// Both at once: two separate blocks, neither swallowing the other.
	if err := os.WriteFile(stamp, []byte("satelle.version: 0.0.100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	both := mustRun(t, testBin, repo, "hook", "context")
	if !strings.Contains(both, "scaffolding drifts") || versionAdvisoryLine(both) == "" {
		t.Errorf("version drift and scaffold drift must BOTH appear:\n%s", both)
	}
}

// versionAdvisoryLine returns the version-stamp advisory line from a
// `hook context` payload, or "" when none was injected. Keyed on the two
// sentences versionDriftLine can emit, not on the whole payload, so a scaffold
// or availability line is never mistaken for one.
func versionAdvisoryLine(payload string) string {
	for _, ln := range strings.Split(payload, "\n") {
		if strings.Contains(ln, "is ahead of this repo's .satelle/deployed.version stamp") ||
			strings.Contains(ln, "no .satelle/deployed.version stamp") {
			return strings.TrimSpace(ln)
		}
	}
	return ""
}

// testBinIsUnreleasedBuild reports the `<ver>+<sha>[-dirty]` form scripts/build-version.sh
// stamps into an unreleased `make build` tree. isDevVersion treats that as a dev
// build and short-circuits every version-gated surface, but isReleaseTestBin
// keys only on the `0.0.0-dev` sentinel, so a SATELLE_BIN from `make build`
// slips past it and would fail this test for the wrong reason. Guard on the rule
// that actually governs the behaviour under test.
func testBinIsUnreleasedBuild(t *testing.T) bool {
	t.Helper()
	return strings.Contains(mustRun(t, testBin, t.TempDir(), "version"), "+")
}
