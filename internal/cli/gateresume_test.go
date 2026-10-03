package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/gatehandle"
)

// A turn that has spent its Stop continuations still gets a finished gate's
// verdict, as the prompt of a resume of the same session (sty_eac9b28d). The Stop
// payloads here are the REAL grok capture (agentcli/testdata/hooks/grok_stop.json)
// with only the fields a turn varies patched in, so a claude-shaped event cannot
// stand in for it.

const grokSession = "01a0e2c7-5729-7a80-bf94-da3779d16c73" // the captured session id

// grokStopFixture is resolved from this file, since a test may change directory.
var grokStopFixture = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "agentcli", "testdata", "hooks", "grok_stop.json")
}()

// grokStopEvent is the captured grok Stop payload with stopHookActive set.
func grokStopEvent(t *testing.T, active bool) string {
	t.Helper()
	b, err := os.ReadFile(grokStopFixture)
	if err != nil {
		t.Fatal(err)
	}
	var ev map[string]any
	if err := json.Unmarshal(b, &ev); err != nil {
		t.Fatal(err)
	}
	ev["stopHookActive"] = active
	out, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// resumeSeams records the watchers the Stop hook starts instead of starting one.
func resumeSeams(t *testing.T) *[]resumeJob {
	t.Helper()
	var jobs []resumeJob
	old := spawnResumeWatcher
	spawnResumeWatcher = func(_ *gatehandle.Store, j resumeJob) error {
		jobs = append(jobs, j)
		return nil
	}
	t.Cleanup(func() { spawnResumeWatcher = old })
	return &jobs
}

// spendContinuations records n Stop emissions already spent this turn.
func spendContinuations(store *gatehandle.Store, n int) {
	for i := 0; i < n; i++ {
		store.AddStopCount(grokSession)
	}
}

func stopOut(t *testing.T, raw string) string {
	t.Helper()
	var out strings.Builder
	if err := runHookStopcheck([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out.String())
}

func finishGate(t *testing.T, store *gatehandle.Store, m gatehandle.Meta, verdict string) {
	t.Helper()
	if err := os.WriteFile(store.VerdictPath(m.ID), []byte(verdict+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(m.ID, gatehandle.Result{}); err != nil {
		t.Fatal(err)
	}
}

// resumeCapture replaces the harness command with one that records what it was
// given. calls counts invocations.
func resumeCapture(t *testing.T) (argvs *[][]string) {
	t.Helper()
	var got [][]string
	old := resumeCommand
	resumeCommand = func(harness string, argv []string) *exec.Cmd {
		got = append(got, argv)
		if runtime.GOOS == "windows" {
			return exec.Command("cmd", "/c", "exit 0")
		}
		return exec.Command("true")
	}
	t.Cleanup(func() { resumeCommand = old })
	return &got
}

// AC1: eight Stop continuations already spent in a turn; the gate finishes later.
// The stop is not blocked a ninth time and nothing rides UserPromptSubmit
// additionalContext; the watcher resumes the same session with the verdict.
//
// The count is set by hand and the Stop hook is called at the cap. Grok does
// not consult hooks for its forced final stop, so this is the defensive branch,
// kept so a hook call at the cap never emits a ninth block. The realistic
// sequence is TestGrokGateStartedAfterTheLastContinuationIsArmedAtHandoff.
func TestGrokSpentTurnDeliversVerdictByResume(t *testing.T) {
	store := stopWakeRepo(t, "50ms")
	t.Setenv(config.SessionEnv, grokSession)
	jobs := resumeSeams(t)
	argvs := resumeCapture(t)
	m := runningGateForSession(t, store, "sty_late", grokSession)
	spendContinuations(store, 8)

	if out := stopOut(t, grokStopEvent(t, true)); out != "" {
		t.Fatalf("a spent turn's Stop emitted %q: that is a ninth continuation", out)
	}
	if got := store.StopCount(grokSession); got != 8 {
		t.Errorf("the count moved to %d without an emission", got)
	}
	if len(*jobs) != 1 || (*jobs)[0].Handle != m.ID || (*jobs)[0].Session != grokSession || (*jobs)[0].Harness != agentcli.HarnessGrok {
		t.Fatalf("no watcher armed for the running gate: %+v", *jobs)
	}
	if store.Delivered(m.ID) {
		t.Fatal("the handle was claimed while it was still running")
	}

	// A later prompt neither claims it nor puts it in additionalContext.
	var prompt strings.Builder
	w := resumeWakeFor([]byte(grokStopEvent(t, false)))
	if w == nil {
		t.Fatal("grok has no resume wake")
	}
	if err := runHookPromptWith(&prompt, w == nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt.String(), m.ID) || store.Delivered(m.ID) {
		t.Fatalf("the prompt hook took the verdict:\n%s", prompt.String())
	}

	finishGate(t, store, m, "accepted in_progress→integration by satelle-code-ac-review")
	if err := runGateResume(store, (*jobs)[0]); err != nil {
		t.Fatal(err)
	}
	if len(*argvs) != 1 {
		t.Fatalf("resume ran %d times, want 1", len(*argvs))
	}
	argv := (*argvs)[0]
	if argv[0] != "grok" || argv[1] != "-p" || !strings.Contains(argv[2], "accepted in_progress→integration") || !strings.Contains(argv[2], m.ID) {
		t.Errorf("the verdict is not the resume prompt: %q", argv)
	}
	if strings.Join(argv[3:], " ") != "--resume "+grokSession+" --permission-mode bypassPermissions" {
		t.Errorf("not a resume of the same session under its own permission mode: %q", argv)
	}
	if !store.Delivered(m.ID) {
		t.Error("the delivered handle was not claimed")
	}
}

// AC3: the handle is claimed once. A second watcher, a second Stop and the
// prompt hook all leave the verdict alone once it has been resumed.
func TestGrokResumeClaimsOnce(t *testing.T) {
	store := stopWakeRepo(t, "50ms")
	t.Setenv(config.SessionEnv, grokSession)
	jobs := resumeSeams(t)
	argvs := resumeCapture(t)
	m := runningGateForSession(t, store, "sty_once", grokSession)
	spendContinuations(store, 8)
	stopOut(t, grokStopEvent(t, true))
	stopOut(t, grokStopEvent(t, true))
	if len(*jobs) != 1 {
		t.Fatalf("two Stops armed %d watchers for one handle", len(*jobs))
	}
	finishGate(t, store, m, "accepted")
	for i := 0; i < 3; i++ {
		if err := runGateResume(store, (*jobs)[0]); err != nil {
			t.Fatal(err)
		}
	}
	if len(*argvs) != 1 {
		t.Fatalf("the verdict was resumed %d times", len(*argvs))
	}
	if out := stopOut(t, grokStopEvent(t, true)); out != "" {
		t.Errorf("a Stop repeated a delivered verdict: %s", out)
	}
}

// AC2: a gate still running when the session stops spends no continuation: no
// block, no note, no systemMessage, and the count does not move.
func TestGrokRunningGateSpendsNoContinuation(t *testing.T) {
	store := stopWakeRepo(t, "50ms")
	t.Setenv(config.SessionEnv, grokSession)
	jobs := resumeSeams(t)
	m := runningGateForSession(t, store, "sty_running", grokSession)
	spendContinuations(store, 2)

	if out := stopOut(t, grokStopEvent(t, true)); out != "" {
		t.Fatalf("a running gate produced Stop output a harness counts: %s", out)
	}
	if got := store.StopCount(grokSession); got != 2 {
		t.Errorf("a running gate moved the count to %d", got)
	}
	if len(*jobs) != 1 || (*jobs)[0].Handle != m.ID {
		t.Fatalf("the running gate has no watcher: %+v", *jobs)
	}
	if store.Delivered(m.ID) {
		t.Error("a running gate was claimed")
	}
}

// A gate that finishes while the turn still has a continuation is delivered by
// the Stop-block, which is counted.
func TestGrokFinishedGateWithinBudgetBlocksAndCounts(t *testing.T) {
	store := stopWakeRepo(t, "10s")
	t.Setenv(config.SessionEnv, grokSession)
	jobs := resumeSeams(t)
	m := runningGateForSession(t, store, "sty_in_budget", grokSession)
	finishGate(t, store, m, "accepted plan→in_progress")
	spendContinuations(store, 3)

	out := stopOut(t, grokStopEvent(t, true))
	var blk stopBlockOut
	if err := json.Unmarshal([]byte(out), &blk); err != nil || blk.Decision != "block" || !strings.Contains(blk.Reason, "accepted plan→in_progress") {
		t.Fatalf("the verdict was not delivered in-turn: %q", out)
	}
	if got := store.StopCount(grokSession); got != 4 {
		t.Errorf("count = %d, want 4: a Stop-block is a continuation", got)
	}
	if len(*jobs) != 0 {
		t.Errorf("an in-turn delivery also armed a resume: %+v", *jobs)
	}
}

// A finished gate on a turn with no continuation left is not blocked a ninth
// time; it is resumed. Like the test above this calls the hook at the cap, the
// defensive branch; the realistic path is the hand-off arm.
func TestGrokFinishedGateOnSpentTurnIsResumed(t *testing.T) {
	store := stopWakeRepo(t, "10s")
	t.Setenv(config.SessionEnv, grokSession)
	jobs := resumeSeams(t)
	argvs := resumeCapture(t)
	m := runningGateForSession(t, store, "sty_spent", grokSession)
	finishGate(t, store, m, "accepted")
	spendContinuations(store, 8)

	if out := stopOut(t, grokStopEvent(t, true)); out != "" {
		t.Fatalf("a ninth continuation was emitted: %s", out)
	}
	if len(*jobs) != 1 {
		t.Fatalf("the finished gate was not handed to a resume: %+v", *jobs)
	}
	if err := runGateResume(store, (*jobs)[0]); err != nil {
		t.Fatal(err)
	}
	if len(*argvs) != 1 || !strings.Contains((*argvs)[0][2], m.ID) {
		t.Fatalf("resume argv = %q", *argvs)
	}
}

// The realistic end of a spent turn: grok does not consult the Stop hook for the
// forced stop after the last continuation, so a gate started after it (or one
// that finishes after it) is never seen by a Stop. The driver's own hand-off arms
// the watcher, from what the hooks that did run recorded about the session.
func TestGrokGateStartedAfterTheLastContinuationIsArmedAtHandoff(t *testing.T) {
	store := stopWakeRepo(t, "50ms")
	t.Setenv(config.SessionEnv, grokSession)
	jobs := resumeSeams(t)
	argvs := resumeCapture(t)

	// Gate A finishes at the seventh emission; the eighth Stop-block carries it.
	a := runningGateForSession(t, store, "sty_a", grokSession)
	finishGate(t, store, a, "accepted A")
	spendContinuations(store, 7)
	if out := stopOut(t, grokStopEvent(t, true)); !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("the eighth emission was not the block: %q", out)
	}
	if got := store.StopCount(grokSession); got != 8 {
		t.Fatalf("count = %d, want 8", got)
	}

	// The model starts gate B and ends the turn. No hook runs for the forced stop.
	b := runningGateForSession(t, store, "sty_b", grokSession)
	b.Harness = agentcli.HarnessGrok
	armResumeForPending(store, b)
	if len(*jobs) != 1 || (*jobs)[0].Handle != b.ID || (*jobs)[0].Session != grokSession {
		t.Fatalf("the late hand-off armed no watcher: %+v", *jobs)
	}
	armResumeForPending(store, b)
	if len(*jobs) != 1 {
		t.Fatalf("a second hand-off armed a second watcher: %+v", *jobs)
	}

	finishGate(t, store, b, "accepted B")
	if err := runGateResume(store, (*jobs)[0]); err != nil {
		t.Fatal(err)
	}
	if len(*argvs) != 1 || !strings.Contains((*argvs)[0][2], "accepted B") {
		t.Fatalf("B's verdict was not resumed: %q", *argvs)
	}
}

// A hand-off by a session no hook has spoken for, or by a harness with no resume
// path, arms nothing and keeps today's delivery.
func TestHandoffArmsNothingWithoutAResumeSession(t *testing.T) {
	store := stopWakeRepo(t, "50ms")
	t.Setenv(config.SessionEnv, grokSession)
	jobs := resumeSeams(t)
	m := runningGateForSession(t, store, "sty_unknown", grokSession)
	m.Harness = agentcli.HarnessGrok
	armResumeForPending(store, m) // no hook has recorded this session
	m.Harness = agentcli.HarnessClaude
	armResumeForPending(store, m)
	if len(*jobs) != 0 {
		t.Fatalf("armed a watcher without a recorded resume session: %+v", *jobs)
	}
}

// The first Stop of a turn (stopHookActive false) starts a fresh count, so a long
// session is not treated as spent for ever.
func TestGrokFirstStopOfATurnResetsTheCount(t *testing.T) {
	store := stopWakeRepo(t, "10s")
	t.Setenv(config.SessionEnv, grokSession)
	resumeSeams(t)
	spendContinuations(store, 8)
	m := runningGateForSession(t, store, "sty_fresh", grokSession)
	finishGate(t, store, m, "accepted")
	out := stopOut(t, grokStopEvent(t, false))
	if !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("a fresh turn could not carry the verdict in-turn: %q", out)
	}
	if got := store.StopCount(grokSession); got != 1 {
		t.Errorf("count = %d, want 1 after the reset and one block", got)
	}
}

// Every Stop emission the harness counts is counted: the dirty-tree block and
// the sibling-seat note, not only the gate verdict.
func TestGrokStopNotesAndBlocksAreCounted(t *testing.T) {
	for name, tc := range map[string]struct {
		seat stopcheckSeatState
		want string
	}{
		"block": {seatNone, `"decision":"block"`},
		"note":  {seatOther, `"systemMessage"`},
	} {
		t.Run(name, func(t *testing.T) {
			repo, _ := stopcheckRepo(t, tc.seat)
			dirtyTree(t, repo)
			store := gateStoreForTest(t)
			// The first Stop of a turn: stopcheck only speaks there (it never
			// re-blocks a continuation), and the count starts from zero.
			out := stopOut(t, grokStopEvent(t, false))
			if !strings.Contains(out, tc.want) {
				t.Fatalf("stopcheck emitted %q, want %s", out, tc.want)
			}
			if got := store.StopCount(grokSession); got != 1 {
				t.Errorf("count = %d, want 1: the emission was not counted", got)
			}
		})
	}
}

// The user's next prompt is a fresh turn.
func TestGrokPromptHookResetsTheCount(t *testing.T) {
	store := stopWakeRepo(t, "10s")
	t.Cleanup(func() { hookHarnessFlag = "" })
	spendContinuations(store, 8)
	if out, err := runRootIn(t, grokStopEvent(t, false), "hook", "prompt", "--harness", "grok"); err != nil {
		t.Fatalf("prompt: %v\n%s", err, out)
	}
	if got := store.StopCount(grokSession); got != 0 {
		t.Errorf("count = %d after a user prompt", got)
	}
}

// A harness with no recorded budget keeps today's behaviour: the still-running
// note, and the prompt hook's delivery. It is never given grok's.
func TestNoResumeWakeForOtherHarnesses(t *testing.T) {
	stopWakeRepo(t, "50ms")
	for _, ev := range []string{"", `{"session_id":"s","hook_event_name":"Stop"}`} {
		if w := resumeWakeFor([]byte(ev)); w != nil {
			t.Errorf("event %q got a resume wake for harness %s", ev, w.harness)
		}
	}
	if _, ok := agentcli.StopResumeFor(agentcli.HarnessClaude); ok {
		t.Error("claude was given a resume path no one measured")
	}
	if got := agentcli.StopResumeUnavailable("claude"); !strings.Contains(got, "claude") {
		t.Errorf("unavailable reason does not name the harness: %s", got)
	}
}

// A resume that cannot start gives the claim back, so another route delivers.
func TestGrokResumeStartFailureReleasesTheClaim(t *testing.T) {
	store := stopWakeRepo(t, "50ms")
	m := runningGateForSession(t, store, "sty_nostart", grokSession)
	finishGate(t, store, m, "accepted")
	old := resumeCommand
	resumeCommand = func(string, []string) *exec.Cmd { return exec.Command(filepath.Join(t.TempDir(), "no-such-grok")) }
	t.Cleanup(func() { resumeCommand = old })
	err := runGateResume(store, resumeJob{Handle: m.ID, Harness: agentcli.HarnessGrok, Session: grokSession, Owner: grokSession})
	if err == nil {
		t.Fatal("a resume that could not start reported success")
	}
	if store.Delivered(m.ID) {
		t.Error("the verdict stayed claimed though nothing delivered it")
	}
}

// End to end through the real watcher: the Stop hook starts a detached
// `satelle hook resume`, and when the gate finishes it runs the harness binary
// with the verdict as the prompt and --resume naming the session. The harness
// binary is a stand-in that records its argv and environment; what grok accepts
// is the documented `grok -p "<prompt>" --resume "<id>"`.
func TestGrokWatcherResumesAfterTheGateFinishes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in harness binary is a shell script")
	}
	store := stopWakeRepo(t, "50ms")
	t.Setenv(config.SessionEnv, grokSession)
	bin := t.TempDir()
	capture := filepath.Join(bin, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + capture + ".tmp\nprintf '%s' \"${GROK_AGENT-unset}\" > " + capture + ".env\nmv " + capture + ".tmp " + capture + "\n"
	if err := os.WriteFile(filepath.Join(bin, "grok"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GROK_AGENT", "1") // the marker grok exports to its hook children
	oldChild := gateChildCommand
	gateChildCommand = func(argv []string) (*exec.Cmd, error) {
		c := exec.Command(os.Args[0], argv...)
		c.Env = append(os.Environ(), "SATELLE_CLI_REEXEC=1")
		return c, nil
	}
	t.Cleanup(func() { gateChildCommand = oldChild })

	m := runningGateForSession(t, store, "sty_e2e", grokSession)
	spendContinuations(store, 8)
	if out := stopOut(t, grokStopEvent(t, true)); out != "" {
		t.Fatalf("Stop emitted %q", out)
	}
	if !store.ResumeArmed(m.ID) {
		t.Fatal("the handle was not armed")
	}
	finishGate(t, store, m, "accepted in_progress→integration")

	var raw []byte
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(capture); err == nil {
			raw = b
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if raw == nil {
		logb, _ := os.ReadFile(filepath.Join(store.Dir(), m.ID, "resume.log"))
		t.Fatalf("the watcher never resumed the session; resume.log:\n%s", logb)
	}
	args := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if args[0] != "-p" {
		t.Fatalf("argv = %q", args)
	}
	prompt := strings.Join(args[1:len(args)-4], "\n")
	if !strings.Contains(prompt, "accepted in_progress→integration") || !strings.Contains(prompt, m.ID) {
		t.Errorf("the verdict is not the prompt: %q", prompt)
	}
	if tail := strings.Join(args[len(args)-4:], " "); tail != "--resume "+grokSession+" --permission-mode bypassPermissions" {
		t.Errorf("argv tail = %q", tail)
	}
	if env, _ := os.ReadFile(capture + ".env"); string(env) != "unset" {
		t.Errorf("the resumed process inherited the session marker: GROK_AGENT=%s", env)
	}
	if !store.Delivered(m.ID) {
		t.Error("the handle was not claimed")
	}
}
