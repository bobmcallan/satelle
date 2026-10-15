package agentcli

import (
	"strings"
	"testing"
)

func TestLauncherSet(t *testing.T) {
	if got := strings.Join(LauncherHarnesses(), ","); got != "claude,grok" {
		t.Errorf("LauncherHarnesses = %q, want claude,grok", got)
	}
	if LauncherBinary(HarnessClaude) != CLIClaude || LauncherBinary(" Grok ") != CLIGrok {
		t.Errorf("launcher binaries = %q / %q", LauncherBinary(HarnessClaude), LauncherBinary(" Grok "))
	}
	for _, h := range []string{"pi", HarnessCursor, "", "unknown"} {
		if HasLauncher(h) {
			t.Errorf("%q must have no launcher", h)
		}
		if !strings.Contains(LaunchStepsUnavailable(h), "has no satelle launcher") {
			t.Errorf("%q: unavailable reason = %q", h, LaunchStepsUnavailable(h))
		}
	}
	if LaunchStepsUnavailable("pi") != "pi has no satelle launcher" {
		t.Errorf("reason does not name the harness: %q", LaunchStepsUnavailable("pi"))
	}
	if LaunchStepsUnavailable(HarnessClaude) != "" {
		t.Error("claude has a launcher")
	}
}

// The capability-table cell reads the same launcher set the launcher itself
// does, so it cannot say available for a harness `agents install` skips.
func TestCapabilityTableLaunchSteps(t *testing.T) {
	for _, row := range CapabilityTable() {
		harness, _, _ := strings.Cut(row.Adapter, " ")
		if row.LaunchSteps.Available != HasLauncher(harness) {
			t.Errorf("%s: launch steps available=%v, launcher=%v", row.Adapter, row.LaunchSteps.Available, HasLauncher(harness))
		}
		if !row.LaunchSteps.Available && !strings.Contains(row.LaunchSteps.Reason, harness) {
			t.Errorf("%s: reason %q does not name the harness", row.Adapter, row.LaunchSteps.Reason)
		}
	}
	if got := launchStepsCapability("pi command").Cell(); got != "unavailable: pi has no satelle launcher" {
		t.Errorf("pi cell = %q", got)
	}
}

func TestComposeLaunchEnv(t *testing.T) {
	base := []string{"PATH=/bin", "FOO_TOKEN=long-lived", "KEEP=1", "FOO_PROFILE=mine"}
	got := ComposeLaunchEnv(base, map[string]string{"FOO_PROFILE": "satelle-ops"}, []string{"FOO_TOKEN", "ABSENT"})
	want := []string{"PATH=/bin", "KEEP=1", "FOO_PROFILE=satelle-ops"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("env = %v, want %v", got, want)
	}
	if got := ComposeLaunchEnv(base, nil, nil); len(got) != len(base) {
		t.Errorf("no overlay and no unset must return base unchanged: %v", got)
	}
}
