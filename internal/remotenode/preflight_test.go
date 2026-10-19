package remotenode

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/remotepreflight"
)

const preflightStory = "sty_preflight"

// preflightRig is the rig with a host.run that tells a probe from the bootstrap:
// the bootstrap command (it execs `satelle remote join`) is recorded and held like
// the node's exec session; anything else is a probe, which the fake host logs as
// "probe <command>" and answers by name — it never evaluates the command, so a
// probe argv can only run if local satelle runs it. A command naming refuse-me
// exits 1, print-env prints an environment holding a credential, print-file prints
// a credential file path; every other probe exits 0.
func newPreflightRig(t *testing.T, lifecycle string) *rig {
	t.Helper()
	r := newRig(t, lifecycle)
	key := base64.StdEncoding.EncodeToString(r.id.Public())
	r.def.Host.Run = []string{"sh", "-c", `case "$2" in
*"remote join"*) printf '%s' "$2" > "$1"; echo "run $0" >> "$3"; echo "installing…"; echo "` + KeyLinePrefix + key + `"; exec sleep 600;;
esac
echo "probe $2" >> "$3"
case "$2" in
*refuse-me*) exit 1;;
*print-env*) echo "API_TOKEN=abc123"; echo "LANG=C";;
*print-file*) echo "/home/dev/.ssh/id_rsa";;
esac
exit 0`, "{label}", r.cmdPath, "{command}", r.logPath}
	return r
}

func (r *rig) startOptions() StartOptions {
	opts := r.options()
	opts.StoryID = preflightStory
	return opts
}

func (r *rig) preflightRows() []ledger.Entry {
	r.t.Helper()
	rows, err := r.ledger.List(context.Background(), ledger.ListFilter{Kind: ledger.KindRemotePreflight, StoryID: preflightStory})
	if err != nil {
		r.t.Fatal(err)
	}
	return rows
}

func probe(expect string, argv ...string) remotedef.Probe {
	return remotedef.Probe{Argv: argv, Expect: expect}
}

// wantRefused asserts err is a *PreflightError and that the start went no further
// than the preflight: the host was stopped, never bootstrapped.
func (r *rig) wantRefused(err error) *PreflightError {
	r.t.Helper()
	var pe *PreflightError
	if !errors.As(err, &pe) {
		r.t.Fatalf("Start error = %v, want a *PreflightError\nhost log: %v", err, r.hostLog())
	}
	log := strings.Join(r.hostLog(), "\n")
	if strings.Contains(log, "run edge-1") {
		r.t.Errorf("the host was bootstrapped despite the failed preflight:\n%s", log)
	}
	if !strings.Contains(log, "stop edge-1") {
		r.t.Errorf("host.stop must run after a failed preflight:\n%s", log)
	}
	if got := r.entries(ledger.KindRemoteStop); len(got) != 1 {
		r.t.Errorf("remote_stop entries = %d, want 1", len(got))
	}
	return pe
}

// AC1: every declared probe goes through host.run on the host, in definition
// order, after host.network and before the bootstrap, and its outcome is compared
// with its expectation.
func TestPreflightRunsEveryProbeThroughHostRunBeforeBootstrap(t *testing.T) {
	r := newPreflightRig(t, "")
	r.def.Steps = []remotedef.Step{
		{Name: "plan", Probes: []remotedef.Probe{probe(remotedef.ExpectSucceed, "check-read", "repo")}},
		{Name: "code", Probes: []remotedef.Probe{probe(remotedef.ExpectRefused, "refuse-me", "push")}},
	}
	r.runNode(echoNode(), readyPrepare("1.2.3"), r.clock)
	run, err := Start(testCtx(t), r.startOptions())
	if err != nil {
		t.Fatalf("Start: %v\nhost log: %v", err, r.hostLog())
	}
	t.Cleanup(func() { _ = run.End(context.Background()) })

	allow := strings.Join(r.options().Allow, ",")
	want := []string{
		"start edge-1",
		"network edge-1 " + allow,
		"probe 'check-read' 'repo'",
		"probe 'refuse-me' 'push'",
		"run edge-1",
	}
	if got := r.hostLog(); !reflect.DeepEqual(got, want) {
		t.Errorf("host commands =\n%v\nwant\n%v", got, want)
	}
	if len(run.Preflight) != 2 || !run.Preflight[0].Pass || !run.Preflight[1].Pass {
		t.Errorf("Preflight = %+v, want two passing checks", run.Preflight)
	}
	if run.Preflight[1].Expect != remotedef.ExpectRefused || run.Preflight[1].Step != "code" {
		t.Errorf("second check = %+v", run.Preflight[1])
	}
}

// AC3 (launcher side): one remote_preflight row per check, on the story given in
// the options, carrying step, argv, expectation and outcome.
func TestPreflightRecordsARowPerCheckOnTheStory(t *testing.T) {
	r := newPreflightRig(t, "")
	r.def.Steps = []remotedef.Step{{Name: "code", Probes: []remotedef.Probe{
		probe(remotedef.ExpectSucceed, "check-read"),
		probe(remotedef.ExpectRefused, "refuse-me"),
	}}}
	r.runNode(echoNode(), readyPrepare("1.2.3"), r.clock)
	run, err := Start(testCtx(t), r.startOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.End(context.Background()) })

	rows := r.preflightRows()
	if len(rows) != 2 {
		t.Fatalf("remote_preflight rows for the story = %d, want 2", len(rows))
	}
	found := false
	for _, row := range rows {
		if row.StoryID != preflightStory {
			t.Errorf("row story = %q", row.StoryID)
		}
		p := string(row.Payload)
		found = found || (strings.Contains(p, `"argv":["refuse-me"]`) && strings.Contains(p, `"expect":"refused"`) &&
			strings.Contains(p, `"step":"code"`) && strings.Contains(p, `"outcome":"pass"`))
	}
	if !found {
		t.Errorf("no row carries the refused probe's step, argv, expectation and outcome: %v", rows)
	}
}

// AC2: a probe whose outcome differs from its expectation stops the start before
// bootstrap, host.stop runs — also for an idle remote — and the error names the
// step and the probe.
func TestPreflightFailedProbeStopsTheStart(t *testing.T) {
	for _, lifecycle := range []string{"", remotedef.LifecycleShutdown, remotedef.LifecycleIdle} {
		t.Run("lifecycle="+lifecycle, func(t *testing.T) {
			r := newPreflightRig(t, lifecycle)
			r.def.Steps = []remotedef.Step{{Name: "code", Probes: []remotedef.Probe{
				// Exits 0 where the definition expects a refusal.
				probe(remotedef.ExpectRefused, "check-write", "secrets"),
			}}}
			_, err := Start(testCtx(t), r.startOptions())
			pe := r.wantRefused(err)
			for _, want := range []string{"code", "check-write secrets"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q should name %q", err, want)
				}
			}
			if len(pe.Checks) != 1 || pe.Checks[0].Pass {
				t.Errorf("checks = %+v", pe.Checks)
			}
			if rows := r.preflightRows(); len(rows) != 1 {
				t.Errorf("a failed check is recorded too: rows = %d, want 1", len(rows))
			}
		})
	}
}

// AC4: no probe argv runs on the developer's machine. The probe would write a
// marker if anything evaluated it; the fake host.run only logs the command, so
// the marker can exist only if local satelle ran the probe itself. The command
// reaches the host shell-quoted, hostile argument included.
func TestPreflightNeverRunsAProbeLocally(t *testing.T) {
	r := newPreflightRig(t, "")
	dir := t.TempDir()
	marker, marker2 := filepath.Join(dir, "marker"), filepath.Join(dir, "marker2")
	r.def.Steps = []remotedef.Step{{Name: "code", Probes: []remotedef.Probe{
		probe(remotedef.ExpectSucceed, "touch", marker),
		probe(remotedef.ExpectSucceed, "echo", "it's; touch "+marker2),
	}}}
	r.runNode(echoNode(), readyPrepare("1.2.3"), r.clock)
	run, err := Start(testCtx(t), r.startOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.End(context.Background()) })

	for _, m := range []string{marker, marker2} {
		if _, err := os.Stat(m); err == nil {
			t.Errorf("%s exists: a probe ran on this machine", m)
		}
	}
	log := strings.Join(r.hostLog(), "\n")
	for _, want := range []string{
		"probe 'touch' '" + marker + "'",
		`probe 'echo' 'it'\''s; touch ` + marker2 + `'`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("host log lacks %q:\n%s", want, log)
		}
	}
}

// AC6: a failing environment check stops the start before bootstrap with
// host.stop run, and neither the error, the ledger nor the checks carry the value
// the host printed.
func TestPreflightEnvCredentialStopsTheStartWithoutLeakingIt(t *testing.T) {
	r := newPreflightRig(t, "")
	r.def.Preflight = remotedef.Preflight{EnvProbe: []string{"print-env"}}
	_, err := Start(testCtx(t), r.startOptions())
	pe := r.wantRefused(err)
	if !strings.Contains(err.Error(), "env") || !strings.Contains(err.Error(), "API_TOKEN") {
		t.Errorf("error %q should name the env check and API_TOKEN", err)
	}
	rows := r.preflightRows()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	for _, s := range []string{err.Error(), string(rows[0].Payload), rows[0].Body, remotepreflight.Describe(pe.Checks[0])} {
		if strings.Contains(s, "abc123") {
			t.Errorf("the credential value leaked: %s", s)
		}
	}
}

// AC6: a failing file check, and a step that holds all three legs, stop the start
// the same way.
func TestPreflightFileAndLegsChecksStopTheStart(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		r := newPreflightRig(t, "")
		r.def.Preflight = remotedef.Preflight{FileProbe: []string{"print-file"}}
		_, err := Start(testCtx(t), r.startOptions())
		r.wantRefused(err)
		if !strings.Contains(err.Error(), "/home/dev/.ssh/id_rsa") {
			t.Errorf("error %q should name the credential file", err)
		}
	})
	t.Run("legs", func(t *testing.T) {
		r := newPreflightRig(t, "")
		r.def.Steps = []remotedef.Step{{Name: "code", Legs: []string{remotedef.LegData, remotedef.LegUntrusted, remotedef.LegOutbound}}}
		_, err := Start(testCtx(t), r.startOptions())
		r.wantRefused(err)
		if !strings.Contains(err.Error(), "code") {
			t.Errorf("error %q should name the step", err)
		}
	})
}

// A definition that declares no preflight starts as before: nothing is run on the
// host ahead of the bootstrap and nothing is recorded.
func TestPreflightNothingDeclaredRunsNothing(t *testing.T) {
	r := newPreflightRig(t, "")
	r.runNode(echoNode(), readyPrepare("1.2.3"), r.clock)
	run, err := Start(testCtx(t), r.startOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.End(context.Background()) })
	if len(run.Preflight) != 0 || len(r.preflightRows()) != 0 {
		t.Errorf("checks = %v, rows = %d", run.Preflight, len(r.preflightRows()))
	}
}
