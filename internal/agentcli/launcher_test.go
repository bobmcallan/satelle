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
		if !strings.Contains(CloudIdentityUnavailable(h), "has no satelle launcher") {
			t.Errorf("%q: unavailable reason = %q", h, CloudIdentityUnavailable(h))
		}
	}
	if CloudIdentityUnavailable("pi") != "pi has no satelle launcher" {
		t.Errorf("reason does not name the harness: %q", CloudIdentityUnavailable("pi"))
	}
	if CloudIdentityUnavailable(HarnessClaude) != "" {
		t.Error("claude has a launcher")
	}
}

// The capability-table cell reads the same launcher set the launcher itself
// does, so it cannot say available for a harness `agents install` skips.
func TestCapabilityTableCloudIdentity(t *testing.T) {
	for _, row := range CapabilityTable() {
		harness, _, _ := strings.Cut(row.Adapter, " ")
		if row.CloudIdentity.Available != HasLauncher(harness) {
			t.Errorf("%s: cloud identity available=%v, launcher=%v", row.Adapter, row.CloudIdentity.Available, HasLauncher(harness))
		}
		if !row.CloudIdentity.Available && !strings.Contains(row.CloudIdentity.Reason, harness) {
			t.Errorf("%s: reason %q does not name the harness", row.Adapter, row.CloudIdentity.Reason)
		}
	}
	if got := cloudIdentityCapability("pi command").Cell(); got != "unavailable: pi has no satelle launcher" {
		t.Errorf("pi cell = %q", got)
	}
}

func TestComposeLaunchEnv(t *testing.T) {
	base := []string{"PATH=/bin", "AWS_ACCESS_KEY_ID=long-lived", "KEEP=1", "AWS_PROFILE=mine"}
	got := ComposeLaunchEnv(base, map[string]string{"AWS_PROFILE": "satelle-ops"}, []string{"AWS_ACCESS_KEY_ID", "ABSENT"})
	want := []string{"PATH=/bin", "KEEP=1", "AWS_PROFILE=satelle-ops"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("env = %v, want %v", got, want)
	}
	if got := ComposeLaunchEnv(base, nil, nil); len(got) != len(base) {
		t.Errorf("no overlay and no unset must return base unchanged: %v", got)
	}
}
