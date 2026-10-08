package verb_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/logsread"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// recoverFixture is the sty_992cffc6 shape: a story engaged in a git tree whose
// dispatch wrote files and then stopped, leaving a dispatch log and nothing else.
type recoverFixture struct {
	dir     string
	logsDir string
	story   workitem.Item
}

func newRecoverFixture(t *testing.T) recoverFixture {
	t.Helper()
	withWiring(t)
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "user.email", "t@t")
	run("git", "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "base.txt")
	run("git", "commit", "-m", "init")

	// The dispatch log resolves through the cwd's .satelle/logs pointer, exactly
	// as in a real repo. Keep it out of the slice the way a real repo does.
	logsDir := filepath.Join(t.TempDir(), "logs")
	if err := os.MkdirAll(filepath.Join(logsDir, "dispatch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(logsDir, filepath.Join(dir, ".satelle", "logs")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte(".satelle/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	wireWithWorkflows(t, routeHalves(
		`["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked" }
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`))
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "recover slice", "body": "goal", "acceptance_criteria": "1. x",
		"category": "feature", "tags": []string{"workflow:eng"},
	}), &it)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	return recoverFixture{dir: dir, logsDir: logsDir, story: it}
}

// writeFile lands a file in the tree, as the dead dispatch did.
func (f recoverFixture) writeFile(t *testing.T, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte("written\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeLog lands a dispatch log whose events are the given "<kind>\t<details>"
// lines (agentcli.FormatEvent's shape), all stamped ago in the past, and
// backdates its mtime to match.
func (f recoverFixture) writeLog(t *testing.T, ago time.Duration, events ...string) string {
	t.Helper()
	at := time.Now().Add(-ago)
	var b strings.Builder
	for _, e := range events {
		fmt.Fprintf(&b, "%s\t%s\n", at.UTC().Format(time.RFC3339Nano), e)
	}
	name := logsread.FormatDispatchName("coder", f.story.ID, at.Add(-time.Minute).UnixNano())
	p := filepath.Join(f.logsDir, "dispatch", name)
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f recoverFixture) recover(t *testing.T, extra map[string]any) verb.RecoverResult {
	t.Helper()
	req := map[string]any{"id": f.story.ID}
	for k, v := range extra {
		req[k] = v
	}
	var res verb.RecoverResult
	if err := json.Unmarshal(call(t, "story-recover", req), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func recoveryRows(t *testing.T, id string) []ledger.Entry {
	t.Helper()
	var rows []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": id, "kind": ledger.KindRecoveryChoice}), &rows)
	return rows
}

// TestRecoverReportsDeadDispatchSlice (AC2/AC3/AC4): the sty_992cffc6 shape — a
// dispatch that wrote files and then died on an open tool call, with no
// completion row — names those files and the last event with its wall time, and
// never says the work is complete.
func TestRecoverReportsDeadDispatchSlice(t *testing.T) {
	f := newRecoverFixture(t)
	f.writeFile(t, "internal_change.go")
	f.writeFile(t, "internal_change_test.go")
	// the open call: a tool_start for Bash with no tool_end and no completed
	f.writeLog(t, 10*time.Minute, "start\t", "tool_start\ttool=Bash")

	res := f.recover(t, nil)
	if res.State != "ended-without-completion" {
		t.Fatalf("state = %q, want ended-without-completion\n%s", res.State, res.Report)
	}
	for _, want := range []string{
		"internal_change.go", "internal_change_test.go", // the files it wrote
		"tool_start", "Bash", // its last recorded event
		"10m", // wall time since
		"did NOT commit", "UNVERIFIED",
	} {
		if !strings.Contains(res.Report, want) {
			t.Errorf("report missing %q:\n%s", want, res.Report)
		}
	}
	assertNoVouching(t, res.Report)
	if !strings.Contains(res.Report, "not attributed per-dispatch") {
		t.Errorf("the file list must disclaim per-dispatch attribution:\n%s", res.Report)
	}
}

// TestRecoverQuietWhenDispatchCompleted: a log that ends in a completion is not
// an unfinished dispatch, whatever is in the tree.
func TestRecoverQuietWhenDispatchCompleted(t *testing.T) {
	f := newRecoverFixture(t)
	f.writeFile(t, "done_work.go")
	f.writeLog(t, time.Minute, "start\t", "tool_start\ttool=Bash", "tool_end\ttool=Bash", "completed\t")
	res := f.recover(t, nil)
	if res.State != "none" || !strings.Contains(res.Report, "no unfinished dispatch") {
		t.Fatalf("a completed dispatch must report nothing to recover:\n%s", res.Report)
	}
	if strings.Contains(res.Report, "done_work.go") {
		t.Errorf("a completed dispatch must not list files:\n%s", res.Report)
	}
}

// TestRecoverReportsWatchdogStall (titled case): a stalled dispatch wrote an
// agent-stalled row, and the report still fires — the row corroborates, it never
// suppresses.
func TestRecoverReportsWatchdogStall(t *testing.T) {
	f := newRecoverFixture(t)
	f.writeFile(t, "stalled_work.go")
	f.writeLog(t, 5*time.Minute, "start\t", "tool_start\ttool=Bash")
	if err := verb.AppendTelemetry(context.Background(), f.story.ID, "executor", "agent-stalled", map[string]any{
		"skill": "coder", "idle": "5m0s", "last_event": "tool_start tool=Bash",
	}); err != nil {
		t.Fatal(err)
	}
	res := f.recover(t, nil)
	if res.State != "ended-without-completion" {
		t.Fatalf("a stalled dispatch must still report: state=%q\n%s", res.State, res.Report)
	}
	for _, want := range []string{"agent-stalled", "5m0s", "stalled_work.go", "UNVERIFIED"} {
		if !strings.Contains(res.Report, want) {
			t.Errorf("stall report missing %q:\n%s", want, res.Report)
		}
	}
}

// TestRecoverChoiceRecordsOneRow (AC5): each choice writes exactly one
// recovery_choice row naming the story and the choice, changes no status, and an
// unknown choice writes none.
func TestRecoverChoiceRecordsOneRow(t *testing.T) {
	f := newRecoverFixture(t)
	f.writeFile(t, "slice.go")
	f.writeLog(t, time.Minute, "start\t", "tool_start\ttool=Bash")

	for i, choice := range []string{"redispatch", "finish", "park"} {
		res := f.recover(t, map[string]any{"choice": choice, "reason": "because " + choice})
		if res.Choice != choice || res.LedgerID == "" {
			t.Fatalf("%s: result = %+v", choice, res)
		}
		rows := recoveryRows(t, f.story.ID)
		if len(rows) != i+1 {
			t.Fatalf("%s: want %d recovery_choice rows, got %d", choice, i+1, len(rows))
		}
		last := rows[len(rows)-1]
		if last.StoryID != f.story.ID {
			t.Errorf("%s: row story = %q", choice, last.StoryID)
		}
		var p struct {
			Choice    string `json:"choice"`
			Reason    string `json:"reason"`
			FileCount int    `json:"file_count"`
			LastEvent string `json:"last_event"`
		}
		json.Unmarshal(last.Payload, &p)
		if p.Choice != choice || p.Reason != "because "+choice || p.FileCount != 1 || !strings.Contains(p.LastEvent, "tool_start") {
			t.Errorf("%s: payload = %+v", choice, p)
		}
	}

	if _, err := dispatchRaw(t, "story-recover", map[string]any{"id": f.story.ID, "choice": "resume"}); err == nil {
		t.Fatal("an unknown choice must be refused")
	} else if !strings.Contains(err.Error(), "redispatch, finish, park") {
		t.Errorf("refusal must list the valid choices: %v", err)
	}
	if n := len(recoveryRows(t, f.story.ID)); n != 3 {
		t.Errorf("a refused choice wrote a row: %d rows", n)
	}

	var it workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": f.story.ID}), &it)
	if it.Status != "in_progress" {
		t.Errorf("recording a choice moved the story to %q", it.Status)
	}
}

// TestRecoverWithoutBaselineStillReports: with no usable git anchor the file list
// says unavailable and the header still stands — the command never fails for it.
func TestRecoverWithoutBaselineStillReports(t *testing.T) {
	f := newRecoverFixture(t)
	// A second story that was never engaged has no baseline.
	var other workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "unengaged", "body": "goal", "acceptance_criteria": "1. x",
		"category": "feature", "tags": []string{"workflow:eng"},
	}), &other)
	f.story = other
	f.writeLog(t, time.Minute, "start\t", "tool_start\ttool=Bash")
	res := f.recover(t, nil)
	if !strings.Contains(res.Report, "unavailable") || !strings.Contains(res.Report, "UNVERIFIED") {
		t.Errorf("no-baseline report:\n%s", res.Report)
	}
}

// assertNoVouching fails when a report claims anything about the work being
// complete. The words may only appear inside the fixed disclaimers.
func assertNoVouching(t *testing.T, report string) {
	t.Helper()
	scrubbed := report
	for _, allowed := range []string{
		"ended WITHOUT a completion", "does not say whether the slice is coherent",
	} {
		scrubbed = strings.ReplaceAll(scrubbed, allowed, "")
	}
	for _, claim := range []string{"complete", "finished", "ready", "done", "coherent"} {
		if strings.Contains(strings.ToLower(scrubbed), claim) {
			t.Errorf("report vouches for the work (%q):\n%s", claim, report)
		}
	}
}
