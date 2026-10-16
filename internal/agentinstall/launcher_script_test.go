package agentinstall

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// Content switches on agentcli's launcher set: every harness in it has a
// script, and nothing outside it does.
func TestContentFollowsLauncherSet(t *testing.T) {
	for _, h := range agentcli.LauncherHarnesses() {
		body, err := Content(h)
		if err != nil {
			t.Fatalf("Content(%s): %v", h, err)
		}
		for _, want := range []string{
			MarkerPrefix,
			"exec " + agentcli.LauncherBinary(h) + ` "$@"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s launcher lacks %q:\n%s", h, want, body)
			}
		}
		if strings.Contains(body, "satelle agents launch") {
			t.Errorf("%s launcher calls back into satelle:\n%s", h, body)
		}
	}
	for _, h := range []string{"pi", agentcli.HarnessCursor, "nope"} {
		if _, err := Content(h); err == nil {
			t.Errorf("Content(%s) produced a launcher", h)
		}
	}
	if strings.Join(Agents(), ",") != strings.Join(agentcli.LauncherHarnesses(), ",") {
		t.Errorf("Agents() = %v, want the launcher set", Agents())
	}
}

func TestNoLauncherResultNamesTheAdapter(t *testing.T) {
	for _, h := range []string{"pi", agentcli.HarnessCursor} {
		r := NoLauncherResult(h)
		if r.Path != "" || r.Action != "skipped" || r.Note != "no satelle launcher for "+h {
			t.Errorf("%s: %+v", h, r)
		}
	}
}

// runLauncher executes the installed script with PATH limited to bin.
func runLauncher(t *testing.T, name, bin string, args ...string) {
	t.Helper()
	home := t.TempDir()
	if _, err := Install(home, name); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(LauncherPath(home, name), args...)
	cmd.Env = []string{"PATH=" + bin}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("launcher failed: %v\n%s", err, out)
	}
}

func fakeBin(t *testing.T, names ...string) (dir, log string) {
	t.Helper()
	dir = t.TempDir()
	log = filepath.Join(t.TempDir(), "calls")
	for _, n := range names {
		body := "#!/bin/sh\necho \"" + n + " $*\" >> \"" + log + "\"\n"
		if err := os.WriteFile(filepath.Join(dir, n), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir, log
}

// The launcher execs the bare harness whether or not satelle is on PATH: it never
// calls back into satelle.
func TestLauncherScriptDispatch(t *testing.T) {
	for _, h := range agentcli.LauncherHarnesses() {
		for _, onPath := range [][]string{{h}, {"satelle", h}} {
			t.Run(h+"/"+strings.Join(onPath, "+"), func(t *testing.T) {
				bin, log := fakeBin(t, onPath...)
				runLauncher(t, h, bin, "--resume", "x")
				got, _ := os.ReadFile(log)
				if want := h + " --resume x\n"; string(got) != want {
					t.Errorf("calls = %q, want %q", got, want)
				}
			})
		}
	}
}
