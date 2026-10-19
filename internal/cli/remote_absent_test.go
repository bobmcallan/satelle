package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
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
