package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// A repo with no remote behaves as before: init, a hook and a story transition
// run, none of them mention a remote or need one, and nothing creates the
// remotes directory.
func TestRepoWithNoRemoteIsUnaffected(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	writeRoute(t, wfDir, closeRouteDone, closeRouteStep)
	gitInitCommit(t, repo)

	var outs []string
	run := func(args ...string) string {
		t.Helper()
		out, err := runRoot(t, args...)
		if err != nil {
			t.Fatalf("satelle %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		outs = append(outs, out)
		return out
	}
	run("init")
	hook, err := runRootIn(t, `{"hook_event_name":"SessionStart","source":"startup"}`, "hook", "context")
	if err != nil {
		t.Fatalf("hook context: %v\n%s", err, hook)
	}
	outs = append(outs, hook)
	run("reindex")
	id := idOf(t, run("story", "create", "--title", "no remote anywhere", "--body", "goal",
		"--acceptance", "1. ok", "--category", "chore"))
	run("story", "set", id, "--status", "in_progress")

	for _, out := range outs {
		for _, marker := range []string{"config/remotes", "satelle remote", "wake_key", "sign in", "sign-in"} {
			if strings.Contains(strings.ToLower(out), marker) {
				t.Errorf("output mentions %q with no remote defined:\n%s", marker, out)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", filepath.FromSlash(config.RemotesDir))); !os.IsNotExist(err) {
		t.Errorf("nothing may create the remotes directory (err=%v)", err)
	}

	// The remote commands themselves are quiet and succeed with nothing defined.
	if out := run("remote", "validate"); !strings.Contains(out, "no remotes defined") {
		t.Errorf("validate with no remotes: %q", out)
	}
}

// The hosted remote verbs name the missing condition: no team workspace is the
// publish message, a workspace with no stored sign-in is the login message, and
// create and validate need neither.
func TestRemoteHostedVerbsNameTheMissingCondition(t *testing.T) {
	hostedVerbs := [][]string{
		{"remote", "publish", "edge"},
		{"remote", "list"},
		{"remote", "fetch", "edge"},
	}
	t.Run("no team workspace", func(t *testing.T) {
		repo := tempRepo(t)
		t.Chdir(repo)
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		if _, err := runRoot(t, "remote", "create", "edge"); err != nil {
			t.Fatal(err)
		}
		want := "no team workspace"
		for _, args := range hostedVerbs {
			out, err := runRoot(t, append(args, "--server", "http://127.0.0.1:1")...)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%v: want an error containing %q, got err=%v\n%s", args, want, err, out)
			}
		}
		if out, err := runRoot(t, "remote", "validate", "edge"); err != nil {
			t.Errorf("validate must be unchanged: %v\n%s", err, out)
		}
	})
	t.Run("workspace but no stored credential", func(t *testing.T) {
		repo := tempRepo(t)
		if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"),
			[]byte("[review]\ngate_create = false\n[hosted]\nworkspace = \"Acme\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Chdir(repo)
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv(hosted.SessionTokenEnv, "")
		if _, err := runRoot(t, "remote", "create", "edge"); err != nil {
			t.Fatal(err)
		}
		for _, args := range hostedVerbs {
			out, err := runRoot(t, append(args, "--server", "http://127.0.0.1:1")...)
			if !errors.Is(err, hosted.ErrLoginRequired) || !strings.Contains(err.Error(), hosted.ErrLoginRequired.Error()) {
				t.Errorf("%v: want hosted.ErrLoginRequired, got err=%v\n%s", args, err, out)
			}
		}
		if out, err := runRoot(t, "remote", "validate", "edge"); err != nil {
			t.Errorf("validate must be unchanged: %v\n%s", err, out)
		}
	})
}

// The remotes directory sits outside every sync area, so a definition and the
// .env beside it are never synced.
func TestRemotesAreOutsideSyncAreas(t *testing.T) {
	area := strings.SplitN(config.RemotesDir, "/", 2)[0]
	for _, a := range config.SyncAreas {
		if a == area {
			t.Fatalf("%q is a sync area, so remote definitions and .env would sync", area)
		}
	}
}
