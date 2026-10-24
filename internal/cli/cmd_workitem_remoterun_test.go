package cli

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/runstatus"
)

// fakeRunStatus is the status source a story's run with a team remote is read
// from, standing in for satelle.dev.
type fakeRunStatus struct {
	run    runstatus.Run
	found  bool
	err    error
	remote string
}

func (f *fakeRunStatus) StoryRun(_ context.Context, remote, _ string) (runstatus.Run, bool, error) {
	f.remote = remote
	return f.run, f.found, f.err
}

func useRunStatus(t *testing.T, f *fakeRunStatus, resolveErr error) {
	t.Helper()
	prev := remoteRunSource
	remoteRunSource = func(context.Context, config.Config) (runstatus.Source, error) {
		if resolveErr != nil {
			return nil, resolveErr
		}
		return f, nil
	}
	t.Cleanup(func() { remoteRunSource = prev })
}

const remoteRunDone = `["*"]
obligations = ["raised", "coded", "opened", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }
recover = { step = "in_progress", from = ["review"] }
`

const remoteRunStep = `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[opened]
status = "review"
remote_wait = { remote = "pr-team", on = { accept = "done", reject = "in_progress", stopped = "blocked" } }
requires = ["coded"]

[closed]
status = "done"
terminal = true
requires = ["opened"]
`

// storyAtWait is a story moved to the waiting step of a repo whose route
// declares one, with the status source stubbed.
func storyAtWait(t *testing.T) string {
	t.Helper()
	repo := tempRepo(t)
	t.Chdir(repo)
	writeRoute(t, filepath.Join(repo, ".satelle", "workflows"), remoteRunDone, remoteRunStep)
	if out, err := runRootIn(t, "", "reindex"); err != nil {
		t.Fatalf("reindex: %v\n%s", err, out)
	}
	out, err := runRootIn(t, "", "story", "create", "--title", "wait on the remote", "--body", "b", "--acceptance", "1. a", "--category", "chore")
	if err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.ID == "" {
		t.Fatalf("story create output: %v\n%s", err, out)
	}
	for _, to := range []string{"in_progress", "review"} {
		if out, err := runRootIn(t, "", "story", "set", created.ID, "--status", to); err != nil {
			t.Fatalf("story set %s: %v\n%s", to, err, out)
		}
	}
	return created.ID
}

// story get and story route say where the story's run with the team remote
// stands: queued with its position, running with its step, concluded with its
// outcome.
func TestStoryGetAndRouteShowTheRemoteRun(t *testing.T) {
	id := storyAtWait(t)
	for _, tc := range []struct {
		name string
		run  runstatus.Run
		want string
	}{
		{"queued", runstatus.Run{State: runstatus.Queued, Position: 3}, "queued with pr-team (position 3)"},
		{"running", runstatus.Run{State: runstatus.Running, Step: "review"}, "running with pr-team at step review"},
		{"concluded", runstatus.Run{State: runstatus.Concluded, Outcome: "accept"}, "concluded with pr-team: accept"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &fakeRunStatus{run: tc.run, found: true}
			useRunStatus(t, src, nil)

			out, err := runRootIn(t, "", "story", "get", id, "--json")
			if err != nil {
				t.Fatalf("story get: %v\n%s", err, out)
			}
			var got struct {
				RemoteRun struct {
					Remote string `json:"remote"`
					Status string `json:"status"`
				} `json:"remote_run"`
			}
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("story get --json is not JSON: %v\n%s", err, out)
			}
			if got.RemoteRun.Remote != "pr-team" || got.RemoteRun.Status != tc.want {
				t.Errorf("story get remote_run = %+v, want pr-team / %q\n%s", got.RemoteRun, tc.want, out)
			}
			if src.remote != "pr-team" {
				t.Errorf("the source was asked for remote %q, want the one the step declares", src.remote)
			}

			out, err = runRootIn(t, "", "story", "route", id)
			if err != nil {
				t.Fatalf("story route: %v\n%s", err, out)
			}
			if !strings.Contains(out, "Remote run** — "+tc.want) {
				t.Errorf("story route must show %q:\n%s", tc.want, out)
			}
		})
	}
}

// A run that cannot be read is shown as unknown with its reason; showing a story
// never fails on the network. A story whose step waits on nothing shows no run.
func TestStoryGetShowsAnUnreadableRemoteRunAsUnknown(t *testing.T) {
	id := storyAtWait(t)
	useRunStatus(t, &fakeRunStatus{err: errors.New("satelle.dev is unreachable")}, nil)
	out, err := runRootIn(t, "", "story", "get", id, "--json")
	if err != nil || !strings.Contains(out, "unknown (satelle.dev is unreachable)") {
		t.Fatalf("an unreadable run is unknown, not an error: %v\n%s", err, out)
	}

	useRunStatus(t, nil, errors.New("no team workspace"))
	if out, err = runRootIn(t, "", "story", "route", id); err != nil || !strings.Contains(out, "unknown (no team workspace)") {
		t.Fatalf("a source that cannot be resolved is unknown, not an error: %v\n%s", err, out)
	}

	if out, err = runRootIn(t, "", "story", "set", id, "--status", "in_progress"); err != nil {
		t.Fatalf("story set: %v\n%s", err, out)
	}
	if out, err = runRootIn(t, "", "story", "get", id, "--json"); err != nil || strings.Contains(out, "remote_run") {
		t.Fatalf("a step that waits on nothing shows no run: %v\n%s", err, out)
	}
}
