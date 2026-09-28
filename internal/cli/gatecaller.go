package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/gatehandle"
	"github.com/bobmcallan/satelle/internal/verb"
)

// Agent-facing gate runs (sty_c4b92c9e, epic:token-accountability).
//
// A verb that runs a reviewer holds its caller for minutes. Called by an agent,
// that is the expensive part: the driving harness backgrounds any command past
// its cutoff (grok: 15s) and the driver re-enters the model to learn how it
// ended, at a full model call each time. So an agent-facing call hands the real
// run to a detached copy of itself, waits a bounded time, and returns either the
// finished verdict block or a handle. The rest of the wait is the harness's:
// hooks deliver the finished verdict into the session (gatedeliver.go). Nothing
// here — or anywhere — answers "is it done yet?" for a handle; that answer is
// the one thing that would let a driver poll.
//
// What this file owns is mechanism only: which caller is which, the bounded
// wait, and the detach. Which gates run and what they judge stay in the
// workflow (satelle-constitution: no gate as code).

// gateModeEnv overrides caller detection: "interactive" keeps today's
// synchronous, progress-on-stderr behaviour (scripts, operators); "agent"
// forces the hand-off.
const gateModeEnv = "SATELLE_GATE_MODE"

// gateWaitEnv overrides the bounded wait with a Go duration. It exists for
// operators and tests; the default is derived from the configured harnesses.
const gateWaitEnv = "SATELLE_GATE_WAIT"

type gateMode string

const (
	gateModeInteractive gateMode = "interactive"
	gateModeAgent       gateMode = "agent"
)

// stderrIsTerminal is a var so tests can stand in for a person at a terminal.
var stderrIsTerminal = func() bool {
	st, err := os.Stderr.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// gateCallerMode decides who is calling. A person at a terminal is interactive;
// anything else — no TTY, or a session a harness identifies — is an agent.
// SATELLE_GATE_MODE overrides both.
func gateCallerMode() gateMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(gateModeEnv))) {
	case string(gateModeInteractive):
		return gateModeInteractive
	case string(gateModeAgent):
		return gateModeAgent
	}
	if config.IsAgentCaller() || !stderrIsTerminal() {
		return gateModeAgent
	}
	return gateModeInteractive
}

// gateRun is what a detached run knows about itself. It is read from the
// environment once, at process start, and the variables are removed at the same
// time: a nested satelle (the reviewer's own shell) inherits the environment, and
// must neither believe it is this run nor record its result.
var gateRun struct{ id, runtime string }

// initGateRun claims the hand-off environment for this process.
func initGateRun() {
	gateRun.id = strings.TrimSpace(os.Getenv(gatehandle.EnvHandle))
	gateRun.runtime = strings.TrimSpace(os.Getenv(gatehandle.EnvRuntime))
	_ = os.Unsetenv(gatehandle.EnvHandle)
	_ = os.Unsetenv(gatehandle.EnvRuntime)
	if inGateRun() && gateRun.runtime != "" {
		// The run's verdict lines go to the handle, where the hand-off reads them,
		// instead of onto a stderr nobody delivers.
		verb.SetVerdictRecorder(appendLine(gatehandle.New(gateRun.runtime).VerdictPath(gateRun.id)))
	}
}

// appendLine returns a sink that appends one line to path per call.
func appendLine(path string) func(string) {
	return func(msg string) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		fmt.Fprintln(f, msg)
	}
}

// inGateRun reports whether this process IS a detached gate run.
func inGateRun() bool { return gateRun.id != "" }

// finishGateRun records how this detached run ended, so the delivery can hand
// the session its verdict. A no-op outside a detached run.
func finishGateRun(err error) {
	if !inGateRun() || gateRun.runtime == "" {
		return
	}
	res := gatehandle.Result{}
	if err != nil {
		res.ExitCode, res.Error = 1, err.Error()
	}
	_ = gatehandle.New(gateRun.runtime).Finish(gateRun.id, res)
}

// gateProgressSink is where the reviewer's one-line progress goes. A person at a
// terminal sees it on stderr, exactly as before. A detached run keeps it in the
// handle's progress log, and an agent-facing call drops it: progress lines in an
// agent's stream are tokens it pays to read and cannot act on.
func gateProgressSink(runtimeDir string) func(string) {
	if inGateRun() {
		return appendLine(gatehandle.New(runtimeDir).ProgressPath(gateRun.id))
	}
	if gateCallerMode() == gateModeAgent {
		return func(string) {}
	}
	return func(msg string) { fmt.Fprintln(os.Stderr, msg) }
}

// errGateHandedOff is what an agent-facing call returns once it has answered
// the caller itself (a replayed verdict, or a handle). It unwinds the command so
// nothing after the dispatch runs twice — the detached copy already ran the
// whole command — and Execute turns it into a clean exit.
var errGateHandedOff = errors.New("gate handed off")

// gateExitError is a replayed failure: the detached run already printed the
// message to the stderr the caller has now been shown, so Execute must not print
// it again.
type gateExitError struct{ msg string }

func (e *gateExitError) Error() string { return e.msg }

// finishExecute maps the two hand-off outcomes onto Execute's contract and
// reports whether err should still be printed.
func finishExecute(err error) (out error, print bool) {
	var ge *gateExitError
	switch {
	case err == nil:
		return nil, false
	case errors.Is(err, errGateHandedOff):
		return nil, false
	case errors.As(err, &ge):
		return err, false
	}
	return err, true
}

// Seams, so tests can run the detached copy in a process they control.
var (
	gateArgv = func() []string { return os.Args[1:] }
	// gateChildCommand builds the detached copy of this command line.
	gateChildCommand = func(argv []string) (*exec.Cmd, error) {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		return exec.Command(exe, argv...), nil
	}
)

// configuredDriverHarnesses is every harness that may be driving this repo: the
// one whose session markers this process carries, and each one the repo carries
// a scaffold for. The wait bound is the shortest cutoff among them, so a repo
// driven from grok and claude never waits as long as claude alone would allow.
func configuredDriverHarnesses(repoRoot string, environ []string) []string {
	set := map[string]bool{}
	for h := range agentcli.DetectSessionHarnesses(environ) {
		set[h] = true
	}
	claude, grok := detectProcessHarnesses(repoRoot, nil)
	if claude {
		set[agentcli.HarnessClaude] = true
	}
	if grok {
		set[agentcli.HarnessGrok] = true
	}
	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// gateWaitBound is how long an agent-facing call may hold the foreground.
func gateWaitBound(repoRoot string) time.Duration {
	if v := strings.TrimSpace(os.Getenv(gateWaitEnv)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
	}
	return agentcli.AgentWaitBound(configuredDriverHarnesses(repoRoot, os.Environ()))
}

// drivingHarness names the harness whose notification path the pending line
// quotes. The session's own published in-loop row wins — its hooks name their
// harness — because the environment can carry a parent agent's markers (a grok
// session started from a Claude Code shell inherits CLAUDECODE=1) and quoting
// claude's wake for a grok driver would claim a notification that never comes.
// The environment is the fallback; "" means no harness was detected.
func drivingHarness() string {
	if id := config.ResolveSession(); id != "" {
		_, exe, _ := config.ResolveSessionModel(id, verb.SessionModelRoleInLoop)
		for _, f := range agentcli.HarnessFactsTable() {
			if f.Harness == exe {
				return exe
			}
		}
	}
	h, _ := agentcli.InLoopHarnessFromEnv(os.Environ())
	return h
}

// handOffGate is the one seam every reviewer-running command goes through
// (sty_c4b92c9e AC6). It returns handled=false when the call should simply run
// here — an interactive caller, the detached run itself, or a hand-off that
// could not start — and handled=true with the error the command must return
// when it has answered the caller.
func handOffGate(cmd *cobra.Command, verbName, storyID string) (handled bool, err error) {
	if inGateRun() || gateCallerMode() != gateModeAgent {
		return false, nil
	}
	a, aerr := appFrom(cmd)
	if aerr != nil || a == nil {
		return false, nil
	}
	store := gatehandle.New(a.RuntimeDir)
	argv := gateArgv()
	meta, cerr := store.Create(gatehandle.Meta{
		Verb: verbName, Story: storyID, Argv: argv,
		Session: config.ResolveSession(), Harness: drivingHarness(),
	})
	if cerr != nil {
		return false, nil
	}
	child, serr := startGateChild(store, meta, a.RuntimeDir)
	if serr != nil {
		// The hand-off could not start: run here, synchronously, rather than
		// refuse the call. Progress stays off the agent's stream either way.
		return false, nil
	}
	// Before child.Wait below: SetPID records the process's creation identity, and
	// until the child is reaped its pid cannot have been reused.
	_ = store.SetPID(meta.ID, child.Process.Pid)
	// The first of the rows a wait's model-call count is read from: the driving
	// session's request count as the gate command is issued.
	if storyID != "" {
		verb.RecordGateWait(cmd.Context(), storyID, costview.GatePhaseIssue, meta.ID, time.Now().UTC())
	}
	done := make(chan struct{})
	go func() { _ = child.Wait(); close(done) }()

	deadline := time.NewTimer(gateWaitBound(a.RepoRoot))
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if v, ok := store.Load(meta.ID); ok {
			return true, replayGate(cmd, store, v)
		}
		select {
		case <-done:
			// The run's process ended. It records its result before it exits, so a
			// handle still without one means it was killed: record that, so the
			// caller is told rather than left on a run that can no longer answer.
			_ = store.Finish(meta.ID, gatehandle.Result{ExitCode: 1, Error: "the gate run's process exited without recording a result"})
			if v, ok := store.Load(meta.ID); ok {
				return true, replayGate(cmd, store, v)
			}
			return true, printPending(cmd, meta)
		case <-deadline.C:
			return true, printPending(cmd, meta)
		case <-cmd.Context().Done():
			return true, printPending(cmd, meta)
		case <-tick.C:
		}
	}
}

// startGateChild starts the detached copy of the command line, its output going
// to the handle's files so the parent's pipes are not held open.
func startGateChild(store *gatehandle.Store, meta gatehandle.Meta, runtimeDir string) (*exec.Cmd, error) {
	c, err := gateChildCommand(meta.Argv)
	if err != nil {
		return nil, err
	}
	out, err := os.OpenFile(store.OutPath(meta.ID), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	errf, err := os.OpenFile(store.ErrPath(meta.ID), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	defer errf.Close()
	c.Stdin = nil
	c.Stdout, c.Stderr = out, errf
	c.Env = append(c.Environ(), gatehandle.EnvHandle+"="+meta.ID, gatehandle.EnvRuntime+"="+runtimeDir)
	detachChild(c)
	if err := c.Start(); err != nil {
		return nil, err
	}
	return c, nil
}

// replayGate hands the caller the finished run's verdict block — the same text
// a hook would deliver later, not the record the command printed — and marks it
// delivered so no hook tells the session twice. A failed run exits non-zero with
// the block on stderr, as a rejected command does.
func replayGate(cmd *cobra.Command, store *gatehandle.Store, v gatehandle.Verdict) error {
	store.Claim(v.Meta.ID)
	if v.Result.ExitCode != 0 {
		msg := verdictBlock(v)
		if msg == "" {
			msg = "gate run failed"
		}
		fmt.Fprintln(cmd.ErrOrStderr(), msg)
		return &gateExitError{msg: msg}
	}
	out := verdictBlock(v)
	if out == "" {
		out = fmt.Sprintf("gate %s completed", v.Meta.ID)
	}
	fmt.Fprintln(cmd.OutOrStdout(), out)
	return errGateHandedOff
}

// pendingPayload is what an agent-facing call prints when the run outlasted the
// wait: a handle and the one thing the driver is to do — nothing.
type pendingPayload struct {
	Gate    string `json:"gate"`
	Handle  string `json:"handle"`
	Verb    string `json:"verb"`
	Story   string `json:"story,omitempty"`
	Notify  bool   `json:"notify"`
	Message string `json:"message"`
}

// pendingMessage is the single line the driver reads. It names the notification
// path when the harness has one, and the harness-named limitation when it does
// not — never a polling instruction, and never a claim of a wake nothing wired.
func pendingMessage(handle, harness string) string {
	facts := agentcli.FactsFor(harness)
	if facts.CompletionNotification.Available {
		return fmt.Sprintf("gate pending: handle %s — the verdict will be delivered into this session as a notification; do not poll, sleep, or re-run this command", handle)
	}
	return fmt.Sprintf("gate pending: handle %s — %s; do not poll, sleep, or re-run this command, the gate keeps running and the verdict is delivered at the next hook this session fires", handle, facts.CompletionNotification.Reason)
}

func printPending(cmd *cobra.Command, meta gatehandle.Meta) error {
	facts := agentcli.FactsFor(meta.Harness)
	p := pendingPayload{
		Gate: "pending", Handle: meta.ID, Verb: meta.Verb, Story: meta.Story,
		Notify:  facts.CompletionNotification.Available,
		Message: pendingMessage(meta.ID, meta.Harness),
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(b))
	return errGateHandedOff
}
