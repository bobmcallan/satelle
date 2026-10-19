//go:build !windows

package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

// TestRemoteWakeRecordsRunAndExportsIds: waking a remote whose story command is a
// stub records one remote_run row per run (failed or not) that `story route`
// lists, the step's process gets the story, step and run ids matching the row and
// none of satelle's markers, and a secret in host.env reaches no ledger row.
func TestRemoteWakeRecordsRunAndExportsIds(t *testing.T) {
	const secret = "s3cr3t-value"
	repo := remoteWithTriggers(t)
	mark := t.TempDir()
	t.Setenv("SATELLE_TEST_MARK", mark)
	t.Setenv("SATELLE_TEST_SECRET", secret)
	t.Setenv(config.DispatchStepEnv, "leaked")
	t.Setenv(config.GateStoryEnv, "leaked")
	// The first run fails, the second records its environment.
	declareStoryHost(t, repo, "if [ -f $MARK_DIR/first ]; then env > $MARK_DIR/env; else touch $MARK_DIR/first; exit 3; fi")

	path := filepath.Join(repo, ".satelle", filepath.FromSlash(config.RemotesDir), "edge", "remote.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for old, repl := range map[string]string{
		"# name = \"your-host-name\"":           "name = \"box-7\"",
		"MARK_DIR = \"${SATELLE_TEST_MARK}\"\n": "MARK_DIR = \"${SATELLE_TEST_MARK}\"\nTOKEN = \"${SATELLE_TEST_SECRET}\"\n",
	} {
		if !strings.Contains(s, old) {
			t.Fatalf("scaffold lacks %q:\n%s", old, s)
		}
		s = strings.Replace(s, old, repl, 1)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	def, err := loadRemoteDef("edge")
	if err != nil {
		t.Fatal(err)
	}
	// The scaffold's segment enters at backlog, a status that holds no seat.
	id := newStory(t, "backlog")

	if out, err := runRootIn(t, `{"trigger_id":"fire_1","remote":"edge"}`, "remote", "wake", "edge"); err == nil {
		t.Fatalf("the first run must fail:\n%s", out)
	}
	if out, err := runRootIn(t, `{"trigger_id":"fire_2","remote":"edge"}`, "remote", "wake", "edge"); err != nil {
		t.Fatalf("wake: %v\n%s", err, out)
	}

	var rows []ledger.Entry
	for _, e := range ledgerRows(t, ledger.KindRemoteRun) {
		if e.StoryID == id {
			rows = append(rows, e)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("want one remote_run row per run, got %d", len(rows))
	}
	var failed, ok ledger.RemoteRunPayload
	if err := json.Unmarshal(rows[0].Payload, &failed); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rows[1].Payload, &ok); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(failed.Error, "exit status 3") || ok.Error != "" {
		t.Errorf("errors = %q / %q, want the first run's exit status only", failed.Error, ok.Error)
	}
	if ok.Remote != "edge" || ok.Version != def.SatelleVersion || ok.Host != "box-7" || ok.Label != def.Identity.Label ||
		ok.Step != "backlog" || !strings.HasPrefix(ok.RunID, "run_") || ok.RunID == failed.RunID {
		t.Errorf("remote_run payload = %+v (definition: version %q, label %q)", ok, def.SatelleVersion, def.Identity.Label)
	}
	started, err1 := time.Parse(time.RFC3339, ok.Started)
	ended, err2 := time.Parse(time.RFC3339, ok.Ended)
	if err1 != nil || err2 != nil || ended.Before(started) {
		t.Errorf("times %q → %q", ok.Started, ok.Ended)
	}

	route, err := runRootIn(t, "", "story", "route", id)
	if err != nil {
		t.Fatalf("route: %v\n%s", err, route)
	}
	want := "- backlog on remote edge @ satelle " + def.SatelleVersion + " · host box-7 · as " + def.Identity.Label
	if n := strings.Count(route, want); n != 2 || !strings.Contains(route, "run "+ok.RunID) {
		t.Errorf("route lists %d of the 2 runs (want %q, run %s):\n%s", n, want, ok.RunID, route)
	}

	envb, err := os.ReadFile(filepath.Join(mark, "env"))
	if err != nil {
		t.Fatal(err)
	}
	env := string(envb)
	for _, line := range []string{config.RemoteStoryEnv + "=" + id, config.RemoteStepEnv + "=backlog", config.RemoteRunEnv + "=" + ok.RunID, "TOKEN=" + secret} {
		if !strings.Contains(env, "\n"+line+"\n") && !strings.HasPrefix(env, line+"\n") {
			t.Errorf("the step's environment lacks %q:\n%s", line, env)
		}
	}
	for _, k := range []string{config.DispatchStepEnv, config.GateStoryEnv} {
		if strings.Contains(env, k+"=") {
			t.Errorf("marker %s reached the step's process", k)
		}
	}

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	all, err := db.Ledger.ListByStory(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 5 {
		t.Fatalf("only %d ledger rows for the story; the check would be vacuous", len(all))
	}
	for _, e := range all {
		if raw, _ := json.Marshal(e); strings.Contains(string(raw), secret) {
			t.Errorf("ledger row %s (%s) carries the host.env secret", e.ID, e.Kind)
		}
	}
}
