package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// sty_a7b2cd3c: `service status` detected a stale service process correctly and
// then left the operator without a next step — and the remedy the story
// originally proposed, `satelle service restart`, did not exist. These tests pin
// both halves: the remedy is printed, and it names a command that is really there.

// TestServiceStatusStaleNamesRemedy (AC1): the mismatch verdict carries the
// remedy, in the `→ fix: ` form the init validator established.
func TestServiceStatusStaleNamesRemedy(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only mechanism")
	}
	setupWantedIdentity(t)
	procRoot := t.TempDir()
	stale := filepath.Join(t.TempDir(), "old-satelle")
	if err := os.WriteFile(stale, []byte("old bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeCgroupPID(t, procRoot, 900, "/system.slice/satelle.service", stale)

	_, matches, suffix := identityVerdict(procRoot, 900)
	if matches {
		t.Fatal("fixture should be a mismatch")
	}
	if !strings.Contains(suffix, "stale process") {
		t.Fatalf("expected the stale verdict, got %q", suffix)
	}
	if !strings.Contains(suffix, "→ fix: satelle service restart") {
		t.Errorf("a stale verdict must name its remedy in the `→ fix: ` form, got:\n%s", suffix)
	}
}

// TestServiceStaleRemedyResolvesToRegisteredCommand (AC2) is the test that makes
// the printed string and the command surface fail TOGETHER. The original story
// proposed `satelle service restart` as the fix when no such command existed —
// exactly the defect a message-only change would reintroduce.
func TestServiceStaleRemedyResolvesToRegisteredCommand(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only mechanism")
	}
	setupWantedIdentity(t)
	procRoot := t.TempDir()
	stale := filepath.Join(t.TempDir(), "old-satelle")
	if err := os.WriteFile(stale, []byte("old bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeCgroupPID(t, procRoot, 900, "/system.slice/satelle.service", stale)
	_, _, suffix := identityVerdict(procRoot, 900)

	const marker = "→ fix: "
	i := strings.Index(suffix, marker)
	if i < 0 {
		t.Fatalf("no remedy in the stale verdict: %q", suffix)
	}
	line := strings.TrimSpace(suffix[i+len(marker):])
	args := strings.Fields(line)
	if len(args) == 0 || args[0] != "satelle" {
		t.Fatalf("remedy should be a satelle command, got %q", line)
	}

	root := NewRootCmd()
	found, _, err := root.Find(args[1:])
	if err != nil {
		t.Fatalf("the remedy %q does not resolve to a registered command: %v", line, err)
	}
	if found == root {
		t.Fatalf("the remedy %q resolved to the root command — it is not a real verb", line)
	}
	if got := found.CommandPath(); got != "satelle service restart" {
		t.Errorf("resolved to %q, want the service restart verb", got)
	}
}

// TestServiceStatusHealthyAndUndeterminedCarryNoRemedy (AC4): only the mismatch
// arm gained the remedy. A matching identity is healthy, and an undetermined one
// has shown nothing to be wrong — telling the operator to restart on no evidence
// would be worse than silence.
func TestServiceStatusHealthyAndUndeterminedCarryNoRemedy(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only mechanism")
	}
	target := setupWantedIdentity(t)

	matchRoot := t.TempDir()
	writeFakeCgroupPID(t, matchRoot, 901, "/system.slice/satelle.service", target)
	_, matches, matchSuffix := identityVerdict(matchRoot, 901)
	if !matches {
		t.Fatalf("fixture should match, got %q", matchSuffix)
	}
	if strings.Contains(matchSuffix, "→ fix:") {
		t.Errorf("a healthy verdict must carry no remedy, got %q", matchSuffix)
	}

	// No such PID anywhere: identity cannot be determined.
	known, _, unknownSuffix := identityVerdict(t.TempDir(), 999)
	if known {
		t.Fatalf("fixture should be undetermined, got %q", unknownSuffix)
	}
	if strings.Contains(unknownSuffix, "→ fix:") {
		t.Errorf("an undetermined verdict must carry no remedy, got %q", unknownSuffix)
	}
}

// AC5 (the no-service-installed path is unchanged) is already covered by
// TestServiceStatusLine_NotInstalled in cmd_service_test.go, which asserts the
// exact "not installed" string. `serviceStatusLine` returns it before any
// identity work, so the remedy added here cannot reach it — and duplicating that
// test would give the same assertion two homes that could drift.

// TestServiceRestartFailsLoudlyWhenStillStale (AC7): the shared restart path
// deliberately SOFT-fails on a non-matching respawn — it prints "could not
// confirm …" and returns nil, which `satelle update` documents. For a verb the
// operator typed specifically to fix a stale process, exiting 0 while the process
// is still stale would be the same class of defect this story is correcting, so
// the verb verifies and fails.
//
// Overriding restartHooks is what proves the SHARED path is the one exercised: a
// reimplementation would not observe these hooks.
func TestServiceRestartFailsLoudlyWhenStillStale(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only mechanism")
	}
	setupWantedIdentity(t)
	procRoot := t.TempDir()
	stale := filepath.Join(t.TempDir(), "old-satelle")
	if err := os.WriteFile(stale, []byte("old bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeCgroupPID(t, procRoot, 900, "/user.slice/user-1000.slice/user@1000.service/app.slice/satelle.service", stale)

	h := noSystemctlPath()
	h.userUnitActive = func() bool { return true }
	h.restartUserUnit = func() error { return nil } // exit 0, but identity still shows the OLD binary
	h.userUnitMainPID = func() int { return 900 }
	withRestartHooks(t, h)

	var buf bytes.Buffer
	err := runServiceRestart(&buf, procRoot)
	if err == nil {
		t.Fatalf("a restart that left the process stale must fail loudly, got nil\noutput:\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "still NOT running the installed binary") {
		t.Errorf("the error must say the process is still stale, got: %v", err)
	}
	// The shared path was genuinely used — its soft message is in the output.
	if !strings.Contains(buf.String(), "could not confirm it is running the new binary") {
		t.Errorf("expected the shared restart path's own reporting, got:\n%s", buf.String())
	}
}

// TestServiceRestartSucceedsWhenIdentityMatches (AC7, the other direction).
func TestServiceRestartSucceedsWhenIdentityMatches(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only mechanism")
	}
	target := setupWantedIdentity(t)
	procRoot := t.TempDir()
	writeFakeCgroupPID(t, procRoot, 901, "/user.slice/user-1000.slice/user@1000.service/app.slice/satelle.service", target)

	h := noSystemctlPath()
	h.userUnitActive = func() bool { return true }
	h.restartUserUnit = func() error { return nil }
	h.userUnitMainPID = func() int { return 901 }
	withRestartHooks(t, h)

	var buf bytes.Buffer
	if err := runServiceRestart(&buf, procRoot); err != nil {
		t.Fatalf("a restart onto the installed binary must succeed: %v\noutput:\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "onto the new binary") {
		t.Errorf("expected the shared path's success reporting, got:\n%s", buf.String())
	}
}
