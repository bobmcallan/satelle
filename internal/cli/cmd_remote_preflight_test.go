package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
)

func (r *remoteTestRig) declare(text string) {
	r.t.Helper()
	path := filepath.Join(r.remote, "remote.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, text...), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *remoteTestRig) preflightRows() []ledger.Entry {
	r.t.Helper()
	out, err := runRoot(r.t, "ledger", "list", "--story", r.story, "--kind", ledger.KindRemotePreflight)
	if err != nil {
		r.t.Fatalf("ledger list: %v\n%s", err, out)
	}
	var rows []ledger.Entry
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		r.t.Fatalf("ledger list is not an entry array: %v\n%s", err, out)
	}
	return rows
}

// `satelle remote test` shows each probe as not run on the stub host, records a
// remote_preflight row per check, and runs no probe and no host command.
func TestRemoteTestShowsPreflightOnStubHost(t *testing.T) {
	r := newRemoteTestRig(t)
	installRemoteAgents(t)
	marker := filepath.Join(filepath.Dir(r.sentinel), "probe-ran")
	r.declare(`
[[step]]
name = "in_progress"

  [[step.probe]]
  argv = ["touch", "` + marker + `"]
  expect = "refused"
`)
	out, err := r.test("--base", "HEAD")
	if err != nil {
		t.Fatalf("remote test: %v\n%s", err, out)
	}
	for _, want := range []string{"preflight for remote edge", "in_progress  probe  expect=refused  not-run  pass", "not run: stub host"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "preflight for remote") > strings.Index(out, "backlog→plan") {
		t.Errorf("the preflight must print before the first edge:\n%s", out)
	}
	if rows := r.preflightRows(); len(rows) != 1 {
		t.Errorf("want one remote_preflight row, got %d", len(rows))
	}
	for _, p := range []string{marker, r.sentinel} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s exists: a probe or host command ran (err=%v)", p, err)
		}
	}
	r.scratchGone()
}

// A step holding all three legs stops the run with every check shown.
func TestRemoteTestStopsOnFailingPreflight(t *testing.T) {
	r := newRemoteTestRig(t)
	agents := installRemoteAgents(t)
	r.declare(`
[[step]]
name = "in_progress"
legs = ["data", "untrusted", "outbound"]
`)
	out, err := r.test("--base", "HEAD")
	if err == nil {
		t.Fatalf("a failing preflight must fail the run:\n%s", out)
	}
	for _, want := range []string{"in_progress  legs", "FAIL", "holds all three legs"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if agents.performs != 0 || agents.reviews != 0 {
		t.Errorf("an agent ran: performs=%d reviews=%d", agents.performs, agents.reviews)
	}
	if len(r.rows()) != 0 {
		t.Errorf("an edge was recorded")
	}
	if rows := r.preflightRows(); len(rows) != 1 {
		t.Errorf("want the failing check recorded, got %d rows", len(rows))
	}
	r.scratchGone()
}
