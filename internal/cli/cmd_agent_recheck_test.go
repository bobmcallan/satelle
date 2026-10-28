package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func stubCursorAgent(t *testing.T, help string) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\ncase \"$1\" in\n  --version) echo 2026.10.22-27dcf5f ;;\n  --help) printf '%s' '" + help + "' ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "cursor-agent"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// AC4: `satelle agent recheck cursor` prints one status line per captured
// contract and exits non-zero when any has changed.
func TestAgentRecheckCursorChangedExitsNonZero(t *testing.T) {
	tempRepo(t)
	stubCursorAgent(t, "Options:\n  -p, --print\n  --mode <mode>\n  --trust\n  --model <model>\n")

	out, err := runRoot(t, "agent", "recheck", "cursor")
	if err == nil || ExitCodeOf(err) == 0 {
		t.Fatalf("a changed contract must exit non-zero, got err=%v\n%s", err, out)
	}
	lines := regexp.MustCompile(`(?m)^(holds|changed|unverifiable)\s+(.+?): `).FindAllStringSubmatch(out, -1)
	got := map[string]string{}
	for _, m := range lines {
		got[m[2]] = m[1]
	}
	for _, c := range []string{"stop cap", "hook timeout handling", "envelope shapes", "ACP methods", "usage fields"} {
		if got[c] == "" {
			t.Errorf("no status line for %q:\n%s", c, out)
		}
	}
	if len(got) != 5 || got["envelope shapes"] != "changed" {
		t.Errorf("want 5 status lines with envelope shapes changed, got %v\n%s", got, out)
	}
	for _, want := range []string{"2026.10.22-27dcf5f", "2026.10.01-e373342"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// AC5: an adapter with no captured version says so and exits zero; an unknown
// harness is refused.
func TestAgentRecheckNoCaptureAndUnknownHarness(t *testing.T) {
	tempRepo(t)
	out, err := runRoot(t, "agent", "recheck", "claude")
	if err != nil || !strings.Contains(out, "no captured version recorded for claude") {
		t.Errorf("claude: err=%v out=%q", err, out)
	}
	if _, err := runRoot(t, "agent", "recheck", "nope"); err == nil {
		t.Error("an unknown harness must be refused")
	}
}
