package agentcli

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// The fixture is the text `claude --cloud` (Claude Code 2.1.291) printed in the
// probe sty_3b112554 recorded as probe-1-evidence, one line per field. A pty
// writes CRLF line ends, which the parse must also take.
func TestParseClaudeCloudOutput(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude_cloud_launch.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := CloudSession{
		ID:    "session_01KDe674AwhRHiL9RAQBuHRK",
		URL:   "https://claude.ai/code/session_01KDe674AwhRHiL9RAQBuHRK",
		Title: "Evidence probe for satelle story sty_3b112554",
	}
	for name, in := range map[string]string{
		"as recorded":        string(raw),
		"pty CRLF and color": "\x1b[?25l" + strings.ReplaceAll(string(raw), "\n", "\r\n") + "\x1b[0m",
	} {
		got, err := ParseClaudeCloudOutput(in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Errorf("%s: got %+v, want %+v", name, got, want)
		}
	}
}

func TestParseClaudeCloudOutputRefusesWithoutASession(t *testing.T) {
	for _, in := range []string{
		"Error: --cloud requires an interactive terminal.\n",
		"Created cloud session: x\nno address printed\n",
		"",
	} {
		if s, err := ParseClaudeCloudOutput(in); err == nil {
			t.Errorf("parsed %q as %+v, want an error", in, s)
		}
	}
}

// A harness with no launcher is explicitly unavailable, naming the adapter, and
// nothing is attempted (no Claude default).
func TestLaunchCloudUnavailableWithoutLauncher(t *testing.T) {
	called := false
	defer SetCloudLauncher(HarnessClaude, func(context.Context, string, string) (CloudSession, error) {
		called = true
		return CloudSession{}, nil
	})()
	for _, h := range []string{HarnessGrok, HarnessPi, HarnessUnknown, ""} {
		_, err := LaunchCloud(context.Background(), h, t.TempDir(), "p")
		var un ErrCloudUnavailable
		if !errors.As(err, &un) || un.Harness != h {
			t.Errorf("harness %q: err = %v, want ErrCloudUnavailable for it", h, err)
		}
		if want := "cloud session: unavailable for adapter " + h; err != nil && err.Error() != want {
			t.Errorf("harness %q: message %q, want %q", h, err, want)
		}
	}
	if called {
		t.Error("the claude launcher ran for another harness")
	}
}

// The branch a cloud session pushes is the adapter's to name; a harness with no
// cloud runner is unavailable, never defaulted.
func TestCloudBranch(t *testing.T) {
	got, err := CloudBranch(HarnessClaude, "sty_ab12cd34", "0f1e2d3c")
	if err != nil || got != "claude/satelle-sty_ab12cd34-0f1e2d3c" {
		t.Fatalf("claude branch = %q, %v", got, err)
	}
	for _, h := range []string{HarnessGrok, HarnessPi, HarnessUnknown, ""} {
		b, err := CloudBranch(h, "sty_x", "n")
		var un ErrCloudUnavailable
		if !errors.As(err, &un) || un.Harness != h || b != "" {
			t.Errorf("harness %q: branch %q err %v, want ErrCloudUnavailable naming it", h, b, err)
		}
	}
}

func TestSetCloudLauncherRestores(t *testing.T) {
	restore := SetCloudLauncher("probe-harness", func(context.Context, string, string) (CloudSession, error) {
		return CloudSession{ID: "session_x"}, nil
	})
	if s, err := LaunchCloud(context.Background(), "probe-harness", "", ""); err != nil || s.ID != "session_x" {
		t.Fatalf("registered launcher not used: %+v %v", s, err)
	}
	restore()
	if _, err := LaunchCloud(context.Background(), "probe-harness", "", ""); err == nil {
		t.Error("launcher still registered after restore")
	}
}

// The cloud-launch cell follows the registry: claude rows are available, every
// grok row is unavailable and names its adapter.
func TestCapabilityTableCloudLaunch(t *testing.T) {
	for _, row := range CapabilityTable() {
		claude := strings.HasPrefix(row.Adapter, "claude ")
		if row.CloudLaunch.Available != claude {
			t.Errorf("%s: cloud launch available=%v, want %v", row.Adapter, row.CloudLaunch.Available, claude)
		}
		if !claude && !strings.Contains(row.CloudLaunch.Reason, row.Adapter) {
			t.Errorf("%s: cloud launch reason %q does not name the adapter", row.Adapter, row.CloudLaunch.Reason)
		}
	}
}
