package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func remoteWaitRouteStep(waitLine string) string {
	return `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[opened]
status = "review"
requires = ["coded"]
` + waitLine + `
[closed]
status = "done"
terminal = true
requires = ["opened"]
`
}

const remoteWaitRouteDone = `["*"]
obligations = ["raised", "coded", "opened", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }
recover = { step = "in_progress", from = ["review"] }
`

// A remote_wait outcome mapped to a status the route declares no edge to is a
// validation failure that names the step, the outcome and the target; a map of
// declared spine, recover and park edges validates clean.
func TestValidateRefusesRemoteWaitUndeclaredEdge(t *testing.T) {
	cases := []struct {
		name, line string
		fail       bool
	}{
		{"undeclared target", `remote_wait = { remote = "pr-team", on = { accept = "release" } }`, true},
		{"declared edges", `remote_wait = { remote = "pr-team", on = { accept = "done", reject = "in_progress", stopped = "blocked" } }`, false},
	}
	for _, tc := range cases {
		for _, verb := range [][]string{{"validate"}, {"workflow", "validate"}} {
			t.Run(tc.name+"/"+strings.Join(verb, " "), func(t *testing.T) {
				repo := tempRepo(t)
				t.Chdir(repo)
				writeRoute(t, filepath.Join(repo, ".satelle", "workflows"), remoteWaitRouteDone, remoteWaitRouteStep(tc.line+"\n"))
				out, err := runRootIn(t, "", verb...)
				if !tc.fail {
					if err != nil || strings.Contains(out, "FAIL  workflows (route)") {
						t.Fatalf("%v: a legal remote_wait map must validate: %v\n%s", verb, err, out)
					}
					return
				}
				if err == nil {
					t.Fatalf("%v: an undeclared remote_wait edge must fail validation:\n%s", verb, out)
				}
				for _, want := range []string{"FAIL  workflows (route)", `"review"`, `"accept"`, `"release"`} {
					if !strings.Contains(out, want) {
						t.Errorf("%v: output must contain %q:\n%s", verb, want, out)
					}
				}
			})
		}
	}
}

// dogfoodPrRouteDone is this repo's [pr] table from .satelle/workflows/done.toml,
// verbatim apart from the comments: the obligations, the park and cancel states
// and the recover edges the route declares.
//
// The "*" table is the fallback every route needs beside it; here it is cut to the
// spine the [pr] steps share, since the real one names release and close steps
// this fixture leaves out.
const dogfoodPrRouteDone = `["*"]
obligations = ["raised", "readied", "coded", "integrated"]
park = { state = "blocked" }
cancel = { state = "cancelled" }

[pr]
obligations = ["raised", "readied", "coded", "integrated", "pr-raised", "pr-opened", "pr-merged"]
park = { state = "blocked", gate = "satelle-story-blocked-review", advisor = "blocked-triage", advisor_skill = "satelle-story-blocked-triage" }
cancel = { state = "cancelled", gate = "satelle-story-cancel-review" }
recover = [
  { step = "in_progress", from = ["integration", "pr_raising", "pr_open"] },
  { step = "backlog", from = ["plan"] },
]
`

// dogfoodPrRouteStep is the step.toml steps those obligations name, as this repo
// declares them, with the one addition under test: [pr-opened] waits on the team
// remote's run for the story and maps its outcomes to edges the route declares.
const dogfoodPrRouteStep = `[raised]
status = "backlog"
start = true

[readied]
status = "plan"
agent = "planner"
reject_budget = 10
requires = ["raised"]

[coded]
status = "in_progress"
agent = "coder"
freeze = true
reject_budget = 10
requires = ["readied"]

[integrated]
status = "integration"
reject_budget = 10
agent = "executor"
requires = ["coded"]

[pr-raised]
status = "pr_raising"
agent = "executor"
reject_budget = 10
requires = ["integrated"]

[pr-opened]
status = "pr_open"
reject_budget = 10
requires = ["pr-raised"]
remote_wait = { remote = "rmt_8ef0a617fbd7", on = { accept = "done", reject = "in_progress", stopped = "blocked" } }

[pr-merged]
status = "done"
terminal = true
reject_budget = 10
requires = ["pr-opened"]
`

// The dogfood PR route, whose denial path today is a remote parking the story,
// expresses the team-remote wait as workflow configuration alone: accept to the
// merge, reject to the recovery step and stopped to the park state validate
// cleanly under both validate verbs, with no binary change.
func TestValidateAcceptsTheDogfoodPrRouteWithRemoteWait(t *testing.T) {
	for _, verb := range [][]string{{"validate"}, {"workflow", "validate"}} {
		t.Run(strings.Join(verb, " "), func(t *testing.T) {
			repo := tempRepo(t)
			t.Chdir(repo)
			writeRoute(t, filepath.Join(repo, ".satelle", "workflows"), dogfoodPrRouteDone, dogfoodPrRouteStep)
			out, err := runRootIn(t, "", verb...)
			if err != nil || strings.Contains(out, "FAIL  workflows (route)") {
				t.Fatalf("%v: the dogfood [pr] route with a remote_wait must validate clean: %v\n%s", verb, err, out)
			}
		})
	}
}
