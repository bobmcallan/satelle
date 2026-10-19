package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
)

// declareStoryCommand gives the rig's remote a host.story_command.
func (r *remoteRig) declareStoryCommand(cmd string) {
	r.t.Helper()
	path := filepath.Join(r.repo, ".satelle", filepath.FromSlash(config.RemotesDir), "edge", "remote.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	s := strings.Replace(string(b), "[host]\n", fmt.Sprintf("[host]\nstory_command = %q\n", cmd), 1)
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		r.t.Fatal(err)
	}
	if out, err := runRoot(r.t, "remote", "validate", "edge"); err != nil {
		r.t.Fatalf("the story command fixture must validate: %v\n%s", err, out)
	}
}

// movingSync stands in for the hosted project: a pull leaves each story at status.
type movingSync struct{ status string }

func (m movingSync) Pull(ctx context.Context, cmd *cobra.Command, ids []string) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := a.Store.Stories.SetStatus(ctx, id, m.status, time.Now()); err != nil {
			return err
		}
	}
	return nil
}

func (m movingSync) Push(context.Context, *cobra.Command, []string) error {
	return fmt.Errorf("the harness must never push")
}

func useRunSync(t *testing.T, s workstateSync) {
	t.Helper()
	prev := remoteRunSync
	remoteRunSync = s
	t.Cleanup(func() { remoteRunSync = prev })
}

func indexAfter(t *testing.T, log string, earlier, later string) {
	t.Helper()
	i, j := strings.Index(log, earlier), strings.Index(log, later)
	if i < 0 || j < 0 || j < i {
		t.Errorf("want %q before %q in the host log:\n%s", earlier, later, log)
	}
}

// AC1/AC2/AC10: a real start brings the node up — host.start, host.network, the
// preflight, then host.run — before any story runs; only the selected story
// runs; a shutdown lifecycle stops the host at the end.
func TestRemoteRunStartsFreshThenRunsSelectedStories(t *testing.T) {
	r := newRemoteRig(t, "shutdown", "fake-agent --system {system} --tools {tools}")
	r.declarePreflight("succeed", "echo preflight-ran >> "+r.hostLog)
	r.declareStoryCommand("echo story {story} >> " + r.hostLog)
	useRunSync(t, movingSync{status: "done"})
	selected := newStory(t, "in_progress")
	other := newStory(t, "in_progress") // at the entry status, but not selected
	record := newStory(t, "backlog")

	out, err := runRoot(t, "remote", "run", "edge", "--server", r.server, "--story", selected, "--story-of-record", record)
	if err != nil {
		t.Fatalf("remote run: %v\n%s", err, out)
	}
	log := r.log()
	indexAfter(t, log, "start edge-1", "network edge-1")
	indexAfter(t, log, "network edge-1", "preflight-ran")
	indexAfter(t, log, "preflight-ran", "story "+selected)
	indexAfter(t, log, "story "+selected, "stop edge-1")
	if strings.Contains(log, "story "+other) {
		t.Errorf("an unselected story at the entry status was run:\n%s", log)
	}
	for _, want := range []string{
		"story " + selected + ": left at done; hand-back ok",
		"session " + selected + ": none logged on the node",
		"spend $0.0000 uncapped",
		"remote edge: stopped",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "gap:") {
		t.Errorf("an unchanged node must report no gap:\n%s", out)
	}
	if rows := remoteRunRows(t, record); len(rows) != 1 || !strings.Contains(string(rows[0].Payload), selected) {
		t.Errorf("remote_run rows on the story of record = %d, want one naming %s", len(rows), selected)
	}
	if got := r.ledgerKinds(ledger.KindRemoteStop); got != 1 {
		t.Errorf("remote_stop entries = %d, want 1", got)
	}
}

// AC10: an idle lifecycle leaves the host running.
func TestRemoteRunIdleLifecycleLeavesTheHost(t *testing.T) {
	r := newRemoteRig(t, "idle", "fake-agent --system {system} --tools {tools}")
	r.declareStoryCommand("echo story {story} >> " + r.hostLog)
	useRunSync(t, movingSync{status: "done"})
	selected, record := newStory(t, "in_progress"), newStory(t, "backlog")

	out, err := runRoot(t, "remote", "run", "edge", "--server", r.server, "--story", selected, "--story-of-record", record)
	if err != nil {
		t.Fatalf("remote run: %v\n%s", err, out)
	}
	if strings.Contains(r.log(), "stop edge-1") || !strings.Contains(out, "remote edge: left idle") {
		t.Errorf("an idle run must not run host.stop and must say so:\n%s\n%s", out, r.log())
	}
}

// AC10: a story command that fails ends the run with an error and the host is
// still stopped; the row on the story of record says what happened.
func TestRemoteRunStopsTheHostAfterARunnerError(t *testing.T) {
	r := newRemoteRig(t, "shutdown", "fake-agent --system {system} --tools {tools}")
	r.declareStoryCommand("exit 7 # {story}")
	useRunSync(t, movingSync{status: "done"})
	selected, record := newStory(t, "in_progress"), newStory(t, "backlog")

	out, err := runRoot(t, "remote", "run", "edge", "--server", r.server, "--story", selected, "--story-of-record", record)
	if err == nil {
		t.Fatalf("a failing story command must fail the run:\n%s", out)
	}
	if !strings.Contains(r.log(), "stop edge-1") || !strings.Contains(out, "remote edge: stopped") {
		t.Errorf("host.stop must run after a runner error:\n%s\n%s", out, r.log())
	}
	if rows := remoteRunRows(t, record); len(rows) != 1 || !strings.Contains(string(rows[0].Payload), `"error"`) {
		t.Errorf("want one remote_run row recording the error, got %d", len(rows))
	}
}

// Selection is one of --story or --tag, and a definition with no story command
// has nothing to run: both are refused before any host command runs.
func TestRemoteRunRefusesBadSelectionBeforeStarting(t *testing.T) {
	r := newRemoteRig(t, "shutdown", "fake-agent --system {system} --tools {tools}")
	record := newStory(t, "backlog")
	for name, args := range map[string][]string{
		"neither":            {"--story-of-record", record},
		"both":               {"--story", record, "--tag", "x", "--story-of-record", record},
		"no story command":   {"--story", record, "--story-of-record", record},
		"no story of record": {"--story", record},
	} {
		if out, err := runRoot(t, append([]string{"remote", "run", "edge", "--server", r.server}, args...)...); err == nil {
			t.Errorf("%s: want a refusal:\n%s", name, out)
		}
	}
	if r.log() != "" {
		t.Errorf("no host command may run for a refused selection:\n%s", r.log())
	}
}

// AC11: the remote group lists run.
func TestHelpRemoteListsRun(t *testing.T) {
	out, err := runRoot(t, "help", "remote")
	if err != nil || !strings.Contains(out, "\n  run ") {
		t.Errorf("satelle help remote must list run (%v):\n%s", err, out)
	}
}
