package remotetest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotepreflight"
)

// declare appends text (preflight and step tables) to the fixture's remote.toml.
func (r *rig) declare(text string) {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.remote, "remote.toml"))
	if err != nil {
		r.t.Fatal(err)
	}
	writeFile(r.t, r.remote, "remote.toml", string(b)+text)
}

func (r *rig) preflightRows() []ledger.AppendInput {
	var out []ledger.AppendInput
	for _, row := range r.rows {
		if row.Kind == ledger.KindRemotePreflight {
			out = append(out, row)
		}
	}
	return out
}

// A must-be-refused probe whose argv would create a marker file is never run by
// the stub host: no marker, the check reads "not run: stub host", the walk goes
// on, and the check is recorded as a ledger row before the first edge.
func TestPreflightStubNeverRunsAProbe(t *testing.T) {
	r := newRig(t)
	marker := filepath.Join(t.TempDir(), "marker")
	r.declare(`
[[step]]
name = "in_progress"

  [[step.probe]]
  argv = ["sh", "-c", "touch ` + marker + `"]
  expect = "refused"
`)
	rep, err := Run(context.Background(), "edge", r.remote, r.item, r.opts())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("the stub host ran a probe (err=%v)", err)
	}
	var probe *remotepreflight.Check
	for i := range rep.Preflight {
		if rep.Preflight[i].Kind == remotepreflight.KindProbe {
			probe = &rep.Preflight[i]
		}
	}
	if probe == nil || probe.Detail != "not run: stub host" || probe.Outcome != remotepreflight.OutcomeNotRun || !probe.Pass {
		t.Fatalf("probe check = %+v", probe)
	}
	if len(rep.Steps) != 3 {
		t.Errorf("the walk must proceed, steps = %+v", rep.Steps)
	}
	rows := r.preflightRows()
	if len(rows) != len(rep.Preflight) || len(rows) == 0 {
		t.Fatalf("want one remote_preflight row per check (%d), got %d", len(rep.Preflight), len(rows))
	}
	var p map[string]any
	if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p["remote"] != "edge" || p["kind"] != "probe" || p["outcome"] != "not-run" || p["expect"] != "refused" {
		t.Errorf("row payload = %v", p)
	}
	// The preflight rows come before the first edge's remote_test row.
	if r.rows[0].Kind != ledger.KindRemotePreflight {
		t.Errorf("first ledger row is %s, want the preflight before any edge", r.rows[0].Kind)
	}
}

// A probe with no expectation is a definition defect: Validate stops the run
// before the preflight, so nothing is dispatched and no preflight row exists.
func TestPreflightIllFormedProbeRefusesTheRun(t *testing.T) {
	r := newRig(t)
	r.declare(`
[[step]]
name = "in_progress"

  [[step.probe]]
  argv = ["probe"]
`)
	rep, err := Run(context.Background(), "edge", r.remote, r.item, r.opts())
	var refused *RefusedError
	if !errors.As(err, &refused) || !strings.Contains(strings.Join(refused.Reasons, "\n"), "step[0].probe[0].expect") {
		t.Fatalf("want a RefusedError naming step[0].probe[0].expect, got %v", err)
	}
	if len(rep.Steps) != 0 || len(rep.Preflight) != 0 {
		t.Errorf("report = %+v", rep)
	}
	r.nothingRan()
}

// A failing check refuses the run before any edge, with every check shown and
// recorded and no scratch tree cut.
func TestPreflightFailureRefusesTheRun(t *testing.T) {
	r := newRig(t)
	r.declare(`
[[step]]
name = "in_progress"
legs = ["data", "untrusted", "outbound"]

  [[step.probe]]
  argv = ["probe"]
  expect = "succeed"
`)
	rep, err := Run(context.Background(), "edge", r.remote, r.item, r.opts())
	var refused *RefusedError
	if !errors.As(err, &refused) || !strings.Contains(strings.Join(refused.Reasons, "\n"), "in_progress legs") {
		t.Fatalf("want a RefusedError naming the step, got %v", err)
	}
	if len(rep.Steps) != 0 {
		t.Errorf("an edge ran: %+v", rep.Steps)
	}
	if len(rep.Preflight) == 0 || len(r.preflightRows()) != len(rep.Preflight) {
		t.Errorf("checks = %d, rows = %d", len(rep.Preflight), len(r.preflightRows()))
	}
	if len(r.s.log) != 0 {
		t.Errorf("an agent ran: %v", r.s.log)
	}
	r.noScratchLeft()
}
