package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentinstall"
	"github.com/bobmcallan/satelle/internal/config"
)

// isolatedLauncherHome points SATELLE_HOME at a fresh directory and returns it, so
// a test controls exactly which launchers exist.
func isolatedLauncherHome(t *testing.T) string {
	t.Helper()
	isolateUserHome(t)
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	return home
}

func writeLauncher(t *testing.T, home, name, body string) string {
	t.Helper()
	path := agentinstall.LauncherPath(home, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// satelle init heals an installed, marker-owned launcher to agentinstall.Content
// (so an upgrade reaches launchers an older binary wrote), leaves an unmarked file
// in place, and creates no launcher that was never installed. It is driven through
// init, not through `agents install`.
func TestInitHealsInstalledLaunchers(t *testing.T) {
	home := isolatedLauncherHome(t)
	stale := "#!/bin/sh\n" + agentinstall.MarkerLine + " claude — do not edit\nexec claude \"$@\"\n"
	claude := writeLauncher(t, home, "claude", stale)
	mine := "#!/bin/sh\necho my own grok wrapper\n"
	grok := writeLauncher(t, home, "grok", mine)

	var out strings.Builder
	if err := runInit(&out, t.TempDir(), true, nil); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	want, err := agentinstall.Content("claude")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(claude); string(got) != want {
		t.Errorf("claude launcher was not rewritten to Content:\n%s", got)
	}
	if got, _ := os.ReadFile(grok); string(got) != mine {
		t.Errorf("an unmarked grok launcher was touched:\n%s", got)
	}
	if !strings.Contains(out.String(), "claude launcher") || !strings.Contains(out.String(), "rewritten") {
		t.Errorf("init should report the rewrite:\n%s", out.String())
	}
}

func TestInitCreatesNoLauncherThatWasNeverInstalled(t *testing.T) {
	home := isolatedLauncherHome(t)
	var out strings.Builder
	if err := runInit(&out, t.TempDir(), true, nil); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, agentinstall.RelBin)); !os.IsNotExist(err) {
		t.Errorf("init created the launcher directory (stat err = %v)", err)
	}
	// The hint is generated from the harnesses that declare launch name flags: it
	// names the launcher path and the directory to put on PATH.
	for _, want := range []string{
		"satelle agents install claude",
		agentinstall.LauncherPath(home, "claude"),
		filepath.Join(home, agentinstall.RelBin),
		"PATH",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("init hint lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "satelle agents install grok") {
		t.Errorf("init hinted a launcher for a harness with no launch name flag:\n%s", out.String())
	}

	// Once the launcher is installed there is nothing to hint at.
	if _, err := agentinstall.Install(home, "claude"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runInit(&out, t.TempDir(), true, nil); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if strings.Contains(out.String(), "session naming") {
		t.Errorf("init hinted at an installed launcher:\n%s", out.String())
	}
}

// `satelle agents install claude` tells the operator how to use the named
// launcher; a harness with no launch name flag gets no such note.
func TestAgentsInstallNamesTheLauncherAndPathDir(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	home := config.GlobalDir()
	out, err := runRoot(t, "agents", "install", "claude")
	if err != nil {
		t.Fatalf("install claude: %v\n%s", err, out)
	}
	for _, want := range []string{
		agentinstall.LauncherPath(home, "claude"),
		filepath.Join(home, agentinstall.RelBin),
		"add " + filepath.Join(home, agentinstall.RelBin) + " to PATH",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("install output lacks %q:\n%s", want, out)
		}
	}
	grokOut, err := runRoot(t, "agents", "install", "grok")
	if err != nil {
		t.Fatalf("install grok: %v\n%s", err, grokOut)
	}
	if strings.Contains(grokOut, "to PATH") {
		t.Errorf("grok has no launch name flag, so no naming note:\n%s", grokOut)
	}
}

func TestHelpSessionTopicNamesLauncherAndPath(t *testing.T) {
	out, err := runRoot(t, "help", "session")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"satelle session name", "satelle agents install claude",
		"agents/bin/satelle-claude", "PATH", "--name", "title: unavailable",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help session lacks %q:\n%s", want, out)
		}
	}
}
