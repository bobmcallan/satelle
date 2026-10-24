package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/runstatus"
	"github.com/bobmcallan/satelle/internal/verb"
)

// A story at a step that waits on a team remote's run (step.toml remote_wait)
// shows where that run stands, wherever the story is shown: the same line `story
// drive` prints. Nothing here names a remote or a step; the route says both.

// remoteRunSource builds the source a story's run with a team remote is read
// from. It is a variable so a test can stand a fake status source in for
// satelle.dev.
var remoteRunSource = hostedRunSource

// hostedRunSource reads runs from satelle.dev as the signed-in user. Remote ids are
// global, so no workspace is resolved.
func hostedRunSource(_ context.Context, _ config.Config) (runstatus.Source, error) {
	server := resolveServer("")
	if server == "" {
		return nil, fmt.Errorf("no hosted server configured — run \"satelle login\"")
	}
	return standingSource{reader: hosted.NewClient(server, hosted.DefaultStore(), nil)}, nil
}

// standingReader is hosted.Client's one reader of a team run.
type standingReader interface {
	TeamRunStatus(ctx context.Context, remoteID, storyID string) (hosted.RunStanding, error)
}

// standingSource adapts hosted.TeamRunStatus to the drive's runstatus.Source.
type standingSource struct{ reader standingReader }

func (s standingSource) StoryRun(ctx context.Context, remote, storyID string) (runstatus.Run, bool, error) {
	st, err := s.reader.TeamRunStatus(ctx, remote, storyID)
	if err != nil {
		return runstatus.Run{}, false, err
	}
	run, ok := runFromStanding(st)
	return run, ok, nil
}

// runFromStanding turns a story's standing with a remote into the run the drive
// reads. ok is false when the remote holds no run for the story. A concluded run
// that recorded no outcome ended without one (killed, failed or unreported): it is
// runstatus.Stopped, its findings "<end_status>: <reason>". A recorded outcome is
// never replaced.
func runFromStanding(st hosted.RunStanding) (runstatus.Run, bool) {
	if st.State == "" || st.State == hosted.RunNone {
		return runstatus.Run{}, false
	}
	run := runstatus.Run{ID: st.NodeID, State: st.State, Position: st.Position, Step: st.Step}
	if st.State == hosted.RunConcluded {
		run.Outcome, run.Findings = st.Outcome, findingsText(st.Findings)
		if run.Outcome == "" {
			run.Outcome = runstatus.Stopped
			run.Findings = stoppedFindings(st.EndStatus, st.Reason)
		}
	}
	return run, true
}

// findingsText is the run's findings as text: a JSON string is unquoted, any other
// value is kept as the JSON it was reported in.
func findingsText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	if t := strings.TrimSpace(string(raw)); t != "null" {
		return t
	}
	return ""
}

// stoppedFindings is "<end_status>: <reason>", with whichever the host left out dropped.
func stoppedFindings(endStatus, reason string) string {
	switch {
	case endStatus != "" && reason != "":
		return endStatus + ": " + reason
	case endStatus != "":
		return endStatus
	}
	return reason
}

// lazyRunSource defers resolving the source to the first run read, so a drive
// that never reaches a waiting step makes no network call and needs no sign-in.
type lazyRunSource struct {
	cfg  config.Config
	once sync.Once
	src  runstatus.Source
	err  error
}

func (l *lazyRunSource) StoryRun(ctx context.Context, remote, storyID string) (runstatus.Run, bool, error) {
	l.once.Do(func() { l.src, l.err = remoteRunSource(ctx, l.cfg) })
	if l.err != nil {
		return runstatus.Run{}, false, l.err
	}
	return l.src.StoryRun(ctx, remote, storyID)
}

// storyRunView is where a story stands with the remote its step waits on.
type storyRunView struct {
	Remote string `json:"remote"`
	// Line is the rendered status, or why it is unknown.
	Line string `json:"status"`
	runstatus.Run
}

// storyRemoteRun reads the run for story id when its step waits on a remote; ok is
// false when the story's step waits on none. A run that cannot be read is
// reported as unknown with the reason, never as an error: showing a story must
// not depend on the network.
func storyRemoteRun(cmd *cobra.Command, id string) (view storyRunView, ok bool) {
	a, err := appFrom(cmd)
	if err != nil || a == nil {
		return storyRunView{}, false
	}
	item, err := a.Store.Stories.Get(cmd.Context(), id)
	if err != nil {
		return storyRunView{}, false
	}
	route, err := verb.GoverningRoute(cmd.Context(), item)
	if err != nil {
		return storyRunView{}, false
	}
	wait, waits := route.Spec.RemoteWaitOf(item.Status)
	if !waits {
		return storyRunView{}, false
	}
	view.Remote = wait.Remote
	src, err := remoteRunSource(cmd.Context(), a.Config)
	if err == nil {
		var run runstatus.Run
		var found bool
		if run, found, err = src.StoryRun(cmd.Context(), wait.Remote, id); err == nil {
			view.Run, view.Line = run, runstatus.Render(run, found, wait.Remote)
			if !found {
				view.Run = runstatus.Run{}
			}
			return view, true
		}
	}
	view.Line = fmt.Sprintf("unknown (%v)", err)
	return view, true
}

// withRemoteRun splices the story's remote run onto a story-get response as a
// `remote_run` member, leaving every other field in its place. A response for a
// story that waits on no remote is returned as it came.
func withRemoteRun(cmd *cobra.Command, id string, resp json.RawMessage) json.RawMessage {
	view, ok := storyRemoteRun(cmd, id)
	if !ok {
		return resp
	}
	s := strings.TrimSpace(string(resp))
	end := strings.LastIndex(s, "}")
	vb, err := json.Marshal(view)
	if !strings.HasPrefix(s, "{") || end < 0 || err != nil {
		return resp
	}
	sep := ","
	if strings.TrimSpace(s[1:end]) == "" {
		sep = ""
	}
	return json.RawMessage(s[:end] + sep + `"remote_run":` + string(vb) + s[end:])
}

// withRemoteRunLine adds the run line to a rendered route: ahead of the outcome
// half when there is one, else at the end.
func withRemoteRunLine(cmd *cobra.Command, id, body string) string {
	view, ok := storyRemoteRun(cmd, id)
	if !ok {
		return body
	}
	line := fmt.Sprintf("**Remote run** — %s", view.Line)
	if head, tail, found := strings.Cut(body, "\n## Outcomes"); found {
		return strings.TrimRight(head, "\n") + "\n\n" + line + "\n\n## Outcomes" + tail
	}
	return strings.TrimRight(body, "\n") + "\n\n" + line + "\n"
}
