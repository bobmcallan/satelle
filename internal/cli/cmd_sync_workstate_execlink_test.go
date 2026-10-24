package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// A team remote node's execution_link row arrives on the subject by a pull, with no
// base, as the node wrote it without the developer's chain: it joins the subject's
// chain without a break, and the subject's route names the execution, its task, the
// remote and the outcome.
func TestWorkstatePullShowsAnExecutionLinkOnTheSubjectsRoute(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, holdOffToml)
	subject := createStoryID(t, "Subject of a remote run")

	seedHostedLedger(f, "led_link01", subject, ledger.KindExecutionLink, ledger.ExecutionLinkPayload{
		Execution: "exe_nightly01", Task: "tsk_nightly-sweep", Remote: "nod_team1", Outcome: "accept",
	}, time.Now().Add(time.Hour).UTC())

	if out, err := runRoot(t, "sync", "workstate", "pull", "--server", ts.URL); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	route, err := runRoot(t, "story", "route", subject)
	if err != nil {
		t.Fatalf("route: %v\n%s", err, route)
	}
	var line string
	for _, ln := range strings.Split(route, "\n") {
		if strings.Contains(ln, "exe_nightly01") {
			line = ln
		}
	}
	if !strings.Contains(route, "## Remote executions") {
		t.Fatalf("route has no Remote executions section:\n%s", route)
	}
	for _, want := range []string{"exe_nightly01", "tsk_nightly-sweep", "nod_team1", "accept"} {
		if !strings.Contains(line, want) {
			t.Errorf("Remote executions line %q does not name %q", line, want)
		}
	}
	if out, err := runRoot(t, "ledger", "verify", "--story", subject); err != nil || !strings.Contains(out, "chain ok") {
		t.Errorf("the subject's chain after the pull: %v\n%s", err, out)
	}
}
