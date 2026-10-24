package cli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/runstatus"
)

type fakeStandings struct {
	st  hosted.RunStanding
	err error
}

func (f fakeStandings) TeamRunStatus(context.Context, string, string) (hosted.RunStanding, error) {
	return f.st, f.err
}

func TestRunFromStandingReadsTheTeamRun(t *testing.T) {
	for name, tc := range map[string]struct {
		st       hosted.RunStanding
		ok       bool
		state    string
		position int
		step     string
		outcome  string
		findings string
	}{
		"none":            {st: hosted.RunStanding{State: hosted.RunNone}},
		"queued":          {st: hosted.RunStanding{State: hosted.RunQueued, Position: 3}, ok: true, state: runstatus.Queued, position: 3},
		"running":         {st: hosted.RunStanding{State: hosted.RunRunning, Step: "review"}, ok: true, state: runstatus.Running, step: "review"},
		"rejected":        {st: hosted.RunStanding{State: hosted.RunConcluded, Outcome: "reject", Findings: json.RawMessage(`"fix the import"`)}, ok: true, state: runstatus.Concluded, outcome: "reject", findings: "fix the import"},
		"accepted":        {st: hosted.RunStanding{State: hosted.RunConcluded, Outcome: "accept", EndStatus: "ended", Reason: "done"}, ok: true, state: runstatus.Concluded, outcome: "accept"},
		"killed":          {st: hosted.RunStanding{State: hosted.RunConcluded, EndStatus: "killed", Reason: "stopped by an operator"}, ok: true, state: runstatus.Concluded, outcome: runstatus.Stopped, findings: "killed: stopped by an operator"},
		"failed bare":     {st: hosted.RunStanding{State: hosted.RunConcluded, EndStatus: "failed"}, ok: true, state: runstatus.Concluded, outcome: runstatus.Stopped, findings: "failed"},
		"structured note": {st: hosted.RunStanding{State: hosted.RunConcluded, Outcome: "reject", Findings: json.RawMessage(`["a","b"]`)}, ok: true, state: runstatus.Concluded, outcome: "reject", findings: `["a","b"]`},
	} {
		run, ok := runFromStanding(tc.st)
		if ok != tc.ok || run.State != tc.state || run.Position != tc.position || run.Step != tc.step ||
			run.Outcome != tc.outcome || run.Findings != tc.findings {
			t.Errorf("%s = %+v, %v; want ok=%v state=%q position=%d step=%q outcome=%q findings=%q",
				name, run, ok, tc.ok, tc.state, tc.position, tc.step, tc.outcome, tc.findings)
		}
	}
}

func TestStandingSourceReadsThroughTeamRunStatus(t *testing.T) {
	run, ok, err := standingSource{fakeStandings{st: hosted.RunStanding{State: hosted.RunQueued, Position: 2}}}.StoryRun(context.Background(), "pr-team", "sty_a")
	if err != nil || !ok || run.State != runstatus.Queued || run.Position != 2 {
		t.Fatalf("queued = %+v, %v, %v", run, ok, err)
	}
	if _, ok, err := (standingSource{fakeStandings{err: hosted.ErrLoginRequired}}).StoryRun(context.Background(), "pr-team", "sty_a"); ok || !errors.Is(err, hosted.ErrLoginRequired) {
		t.Fatalf("a sign-in failure is an error, got ok=%v err=%v", ok, err)
	}
}
