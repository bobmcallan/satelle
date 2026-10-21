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
			"exec " + agentcli.LauncherBinary(h) + " ",
			` "$@"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s launcher lacks %q:\n%s", h, want, body)
			}
		}
		if strings.Contains(body, "satelle agents launch") {
			t.Errorf("%s launcher calls back into satelle:\n%s", h, body)
		}
		// Only a harness that declares a launch name flag asks satelle for a name.
		if asks := strings.Contains(body, "satelle session name"); asks != (len(agentcli.LaunchNameFlags(h)) > 0) {
			t.Errorf("%s launcher asks satelle for a name = %v, flags = %v:\n%s", h, asks, agentcli.LaunchNameFlags(h), body)
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
				want := h + " --resume x\n"
				// A harness that can be named asks satelle (which names nothing here).
				if len(onPath) == 2 && len(agentcli.LaunchNameFlags(h)) > 0 {
					want = "satelle session name\n" + want
				}
				if string(got) != want {
					t.Errorf("calls = %q, want %q", got, want)
				}
			})
		}
	}
}

// A harness with no launch name flag keeps the launcher it always had, byte for
// byte: grok is bare exec, with no call into satelle.
func TestGrokLauncherBodyUnchanged(t *testing.T) {
	want := "#!/bin/sh\n" + MarkerPrefix + " grok — do not edit\nexec grok \"$@\"\n"
	got, err := Content("grok")
	if err != nil || got != want {
		t.Fatalf("grok Content = %q, %v; want %q", got, err, want)
	}
}

// runNamedLauncher runs the installed claude launcher with a stub claude that
// records its argv one element per line and a stub satelle that prints the name
// (or fails), and returns the recorded argv. stubSatelle false leaves satelle off
// PATH.
func runNamedLauncher(t *testing.T, satelle string, stubSatelle bool, args ...string) []string {
	t.Helper()
	bin, argvFile := t.TempDir(), filepath.Join(t.TempDir(), "argv")
	claude := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> \"" + argvFile + "\"; done\n: >> \"" + argvFile + "\"\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	if stubSatelle {
		if err := os.WriteFile(filepath.Join(bin, "satelle"), []byte("#!/bin/sh\n"+satelle+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	home := t.TempDir()
	if _, err := Install(home, "claude"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(LauncherPath(home, "claude"), args...)
	cmd.Env = []string{"PATH=" + bin}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("launcher failed: %v\n%s", err, out)
	}
	raw, _ := os.ReadFile(argvFile)
	s := strings.TrimSuffix(string(raw), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// The claude launcher passes the session name, unless the user named the session
// (any spelling agentcli lists), the name is empty, or satelle fails or is
// missing — in every one of those it execs the bare harness.
func TestClaudeLauncherPassesSessionName(t *testing.T) {
	const name = "box · repo · sty_1"
	say := "echo '" + name + "'"
	cases := []struct {
		label       string
		satelle     string
		stubSatelle bool
		args        []string
		want        []string
	}{
		{"injected", say, true, []string{"--resume", "x"}, []string{"--name", name, "--resume", "x"}},
		{"injected, no args", say, true, nil, []string{"--name", name}},
		{"--name x", say, true, []string{"--name", "mine"}, []string{"--name", "mine"}},
		{"--name=x", say, true, []string{"--name=mine"}, []string{"--name=mine"}},
		{"-n x", say, true, []string{"-n", "mine", "--resume"}, []string{"-n", "mine", "--resume"}},
		{"satelle fails", "exit 1", true, []string{"--resume"}, []string{"--resume"}},
		{"empty name", "echo", true, []string{"--resume"}, []string{"--resume"}},
		{"satelle not on PATH", "", false, []string{"--resume"}, []string{"--resume"}},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			got := runNamedLauncher(t, c.satelle, c.stubSatelle, c.args...)
			if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
				t.Errorf("claude argv = %q, want %q", got, c.want)
			}
		})
	}
}

// Every user-passed spelling agentcli lists for claude suppresses the injection:
// the launcher's guard is generated from LaunchNameFlags, not hardcoded.
func TestClaudeLauncherGuardCoversEveryListedSpelling(t *testing.T) {
	for _, f := range agentcli.LaunchNameFlags("claude") {
		got := runNamedLauncher(t, "echo injected", true, f, "x")
		if want := []string{f, "x"}; strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("%s: argv = %q, want %q", f, got, want)
		}
	}
}

func TestHealRewritesOnlyInstalledMarkedLaunchers(t *testing.T) {
	home := t.TempDir()
	stale := "#!/bin/sh\n" + MarkerLine + " claude — do not edit\nexec claude \"$@\"\n"
	claude := LauncherPath(home, "claude")
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, []byte(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	rs, err := Heal(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Name != "claude" || rs[0].Action != "updated" {
		t.Fatalf("Heal = %+v, want only claude updated", rs)
	}
	if want, _ := Content("claude"); mustRead(t, claude) != want {
		t.Errorf("claude launcher not rewritten to Content")
	}
	if _, err := os.Stat(LauncherPath(home, "grok")); !os.IsNotExist(err) {
		t.Errorf("an uninstalled launcher was created (stat err = %v)", err)
	}
	// An unmarked file is left alone.
	if err := os.WriteFile(claude, []byte("#!/bin/sh\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rs, err = Heal(home)
	if err != nil || len(rs) != 1 || rs[0].Action != "skipped" || mustRead(t, claude) != "#!/bin/sh\necho mine\n" {
		t.Fatalf("Heal over an unmarked file = %+v, %v", rs, err)
	}
}

// pi declares a launch name flag but has no launcher, so there is nothing to tell
// the operator to start: only a launcher harness gets a usage note.
func TestUsageNoteNeedsALauncher(t *testing.T) {
	home := t.TempDir()
	if got := UsageNote(home, "pi"); got != "" {
		t.Errorf("UsageNote(pi) = %q, want none", got)
	}
	if got := UsageNote(home, "claude"); !strings.Contains(got, LauncherPath(home, "claude")) {
		t.Errorf("UsageNote(claude) = %q, want the launcher path", got)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
