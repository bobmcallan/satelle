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
	}
}
