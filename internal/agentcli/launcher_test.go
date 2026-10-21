package agentcli

import (
	"strings"
	"testing"
)

// Every harness has an entry for its launch name flags: claude and pi list
// their two spellings (from each harness's --help), grok and cursor declare none
// (their sessions record an explicit unavailable).
func TestLaunchNameFlags(t *testing.T) {
	want := map[string]string{
		HarnessClaude: "--name,-n",
		HarnessGrok:   "",
		HarnessPi:     "--name,-n",
		HarnessCursor: "",
	}
	for h, flags := range want {
		if got := strings.Join(LaunchNameFlags(h), ","); got != flags {
			t.Errorf("LaunchNameFlags(%s) = %q, want %q", h, got, flags)
		}
	}
	if got := LaunchNameFlags(" Claude "); len(got) != 2 {
		t.Errorf("LaunchNameFlags is not case/space-insensitive: %v", got)
	}
	if got := strings.Join(NameableHarnesses(), ","); got != "claude" {
		t.Errorf("NameableHarnesses = %q, want claude", got)
	}
	// The returned slice is a copy: a caller cannot rewrite the table.
	LaunchNameFlags(HarnessClaude)[0] = "--changed"
	if LaunchNameFlags(HarnessClaude)[0] != "--name" {
		t.Error("LaunchNameFlags exposes its backing table")
	}
}

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
	}
}
