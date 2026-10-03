package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/gatehandle"
)

// Delivery of a finished gate into a session whose turn has a capped number of
// Stop continuations (sty_eac9b28d, epic:gate-wake).
//
// The Stop hook wakes a session by blocking the stop with the verdict as the
// reason. A harness that caps those continuations per turn overrides the
// gate after the last one and does not consult the hook again, and it discards
// the prompt hook's additionalContext, so a verdict that finishes after the cap
// has no in-turn route. The route left is the one the harness documents for
// exactly this: the next user prompt starts a fresh turn. A resume of the same
// session with the verdict as its prompt is that prompt — not a hook's discarded
// context, and not a second session.
//
// Three rules keep it honest:
//   - every Stop emission the harness counts spends a continuation, so the hook
//     counts blocks AND notes, and says nothing at all about a gate that is still
//     running — a still-running note would be a continuation spent on nothing;
//   - a gate still running when the stop is allowed gets one watcher, which waits
//     for the handle to finish, claims it, and resumes the session once;
//   - the claim is the same exclusive Claim every other delivery uses, so a
//     verdict reaches the session once whichever route gets there first, and
//     there is nothing to poll.

// resumeWatchPoll is how often a watcher looks at its handle. It reads a file in
// the runtime directory; it is not a model call and not a status verb.
var resumeWatchPoll = 2 * time.Second

// resumeQuiet is how long a session whose Stop budget is spent must show no
// tool call before it is taken to be between turns. Past the last continuation
// the harness ends the turn without telling any hook, so this is the one signal
// there is; it is a bound, not a measurement of the turn's end.
var resumeQuiet = 2 * time.Minute

// resumeWake is one Stop or prompt hook's view of a session whose harness has a
// recorded continuation cap and resume path. A nil *resumeWake means the harness
// has none, and every method is then the ordinary behaviour.
type resumeWake struct {
	store          *gatehandle.Store
	res            agentcli.StopResume
	harness        string
	owner          string // the identity a handle is stamped with (config.ResolveSession)
	session        string // the harness's own session id — what is resumed
	cwd            string
	permissionMode string
	count          int  // Stop emissions spent this turn
	emitted        bool // this Stop invocation emitted something the harness counts
}

// resumeWakeFor returns the wake for the harness the event came from, nil when
// that harness has no recorded cap and resume path, when the event names no
// session to resume, outside a governed repo, or in a dispatched process (which
// is not the session a verdict is for).
func resumeWakeFor(raw []byte) *resumeWake {
	if isDispatchedProcess() {
		return nil
	}
	h := hookHarnessFlag
	if h == "" {
		h = harnessFromEvent(raw)
	}
	res, ok := agentcli.StopResumeFor(h)
	if !ok {
		return nil
	}
	session := sessionIDFromHook(raw)
	if session == "" {
		return nil
	}
	store, ok := hookGateStore()
	if !ok {
		return nil
	}
	var ev struct {
		Cwd       string `json:"cwd"`
		Mode      string `json:"permissionMode"`
		ModeSnake string `json:"permission_mode"`
	}
	_ = json.Unmarshal(raw, &ev)
	mode := strings.TrimSpace(ev.Mode)
	if mode == "" {
		mode = strings.TrimSpace(ev.ModeSnake)
	}
	w := &resumeWake{
		store: store, res: res, harness: h,
		owner: config.ResolveSession(), session: session,
		cwd: strings.TrimSpace(ev.Cwd), permissionMode: mode,
		count: store.StopCount(session),
	}
	// Every hook the harness calls tells the next gate hand-off who to resume.
	store.NoteSession(w.owner, gatehandle.HarnessSession{Harness: h, Session: session, Cwd: w.cwd, Mode: mode})
	return w
}

// newTurn starts a fresh count: a user prompt, or the first Stop of a turn.
func (w *resumeWake) newTurn() {
	if w == nil {
		return
	}
	w.store.ResetStopCount(w.session)
	w.count = 0
	w.store.OpenTurn(w.session)
}

// activity records that the session is mid-turn: any tool call the harness
// announces. It is how a turn whose end is never announced is told from one
// still going.
func (w *resumeWake) activity() {
	if w != nil {
		w.store.OpenTurn(w.session)
	}
}

// settle runs when a Stop hook returns: a Stop that emitted nothing ended the
// turn, so a watcher may resume the session; a block or a note is a continuation
// and the turn goes on.
func (w *resumeWake) settle() {
	if w != nil && !w.emitted {
		w.store.CloseTurn(w.session)
	}
}

// promptCarriesGates reports whether the prompt hook may put a finished gate's
// verdict in front of the model: a harness with no resume wake always does, and
// one with a wake does only where the harness delivers the prompt hook's
// additionalContext (a harness that discards it would lose a verdict claimed
// there, so its verdict stays for the resume).
func (w *resumeWake) promptCarriesGates() bool {
	return w == nil || agentcli.FactsFor(w.harness).PromptContext.Available
}

// closeTurn records that the harness ended the session's turn on its own — a
// failed turn, or the session itself — and hands every gate of the session that
// is still undelivered to a watcher: no Stop hook will run for it, so the resume
// is the only route left, and it waits for the gate to finish.
func (w *resumeWake) closeTurn() {
	if w == nil {
		return
	}
	w.store.CloseTurn(w.session)
	for _, g := range w.ownedGates() {
		w.arm(g.store, g.id, g.store.Observe(g.id))
	}
}

// ownedGates is every undelivered run of the session, in whichever store holds
// it. The wake's own store is the turn plane; the runs are the handle plane.
func (w *resumeWake) ownedGates() []gateRef {
	return ownedGates(handleStoresFor(w.store, w.owner), w.owner)
}

// spent reports whether this turn can spend no more Stop continuations.
func (w *resumeWake) spent() bool { return w != nil && w.count >= w.res.Cap }

func (w *resumeWake) bump() {
	if w != nil {
		w.count = w.store.AddStopCount(w.session)
		w.emitted = true
	}
}

// block emits a Stop block, counting the continuation it spends.
func (w *resumeWake) block(out io.Writer, reason string) error {
	w.bump()
	return emitStopBlock(out, reason)
}

// note emits non-error Stop feedback. The harness counts it as a continuation
// exactly as it counts a block, so it is counted here too.
func (w *resumeWake) note(out io.Writer, text string) error {
	w.bump()
	return emitStopNote(out, text)
}

// stop is the Stop hook's gate decision. It returns the verdict text to block
// the stop with, or "" to allow the stop. It never returns a still-running note:
// a gate still going when the stop is allowed is handed to a watcher instead, and
// the stop is allowed with no output.
func (w *resumeWake) stop(raw []byte) string {
	if !stopHookActive(raw) {
		w.newTurn() // the first Stop of a turn: the count restarts
	}
	mine := w.ownedGates()
	if len(mine) == 0 {
		return ""
	}
	spent := w.spent()
	if !spent {
		waitForGates(mine, stopGateWait())
	}
	var blocks []string
	var delivered []gatehandle.Meta
	for _, g := range mine {
		o := g.store.Observe(g.id)
		switch {
		case o.Terminal() && !spent:
			if g.store.Claim(g.id) {
				blocks = append(blocks, renderGateVerdict(o.Verdict))
				delivered = append(delivered, o.Verdict.Meta)
			}
		default:
			// Running, unverified, or finished with no continuation left to carry it:
			// the watcher resumes the session once the handle has finished.
			w.arm(g.store, g.id, o)
		}
	}
	recordGateDelivered(delivered)
	return strings.Join(blocks, "\n\n")
}

// resumeJob is what a watcher needs to resume a session with one handle's verdict.
type resumeJob struct {
	Handle, Harness, Session, Owner, Cwd, PermissionMode string
	// ServeRuntime is the runtime dir of the store the session's turn state lives
	// in, set only when the handle lives in a different store (sty_8f10499d). The
	// watcher judges the turn and takes the resume lock there and everything else
	// in the handle's own store; empty means they are one store.
	ServeRuntime string
}

// arm hands a handle that cannot be delivered in this turn to one watcher. The
// handle stays unclaimed until it finishes; it is armed in the store that holds
// it, while the session's turn stays in the wake's own store.
func (w *resumeWake) arm(handle *gatehandle.Store, id string, o gatehandle.Observation) {
	armResume(handle, resumeJob{Handle: id, Harness: w.harness, Session: w.session, Owner: w.owner, Cwd: w.cwd, PermissionMode: w.permissionMode, ServeRuntime: serveRuntimeFor(w.store, handle)}, o)
}

// serveRuntimeFor is the ServeRuntime a job for a handle in handle carries when
// the session's turn state lives in serve: "" when they are the same store.
func serveRuntimeFor(serve, handle *gatehandle.Store) string {
	if filepath.Clean(serve.RuntimeDir()) == filepath.Clean(handle.RuntimeDir()) {
		return ""
	}
	return serve.RuntimeDir()
}

// armResume starts job's watcher unless one already owns the handle.
func armResume(store *gatehandle.Store, job resumeJob, o gatehandle.Observation) {
	if !store.ArmResume(job.Handle) {
		return // a watcher already owns it
	}
	if o.State == gatehandle.RunningUnverified {
		store.MarkNotified(job.Handle, true, o.Reason)
	}
	if err := spawnResumeWatcher(store, job); err != nil {
		store.Disarm(job.Handle) // nothing is waiting: a later arming may try again
	}
}

// armResumeForPending is called when a gate hand-off returns a pending handle to
// the driver. It arms the watcher there, in the driver's own tool call, because
// that is the one place that is certain to run: a harness that caps the Stop
// continuations of a turn does not call the Stop hook once they are spent, so a
// gate started after that — or one that was running when the last continuation
// went — would otherwise never be resumed. The hooks the harness did call have
// recorded who to resume (resumeWakeFor); a harness with no resume path, a
// dispatched process, or a session no hook has spoken for arms nothing and keeps
// today's delivery.
func armResumeForPending(store *gatehandle.Store, meta gatehandle.Meta) {
	if isDispatchedProcess() {
		return
	}
	if _, ok := agentcli.StopResumeFor(meta.Harness); !ok {
		return
	}
	// The hooks that recorded the session ran in the serving repo, which for a
	// run started across repos is not the one holding the handle.
	serve := store
	if meta.ServeRoot != "" {
		s, ok := gateStoreAt(meta.ServeRoot)
		if !ok {
			return
		}
		serve = s
	}
	hs, ok := serve.SessionFor(meta.Session)
	if !ok || hs.Harness != meta.Harness {
		return
	}
	armResume(store, resumeJob{Handle: meta.ID, Harness: hs.Harness, Session: hs.Session, Owner: meta.Session, Cwd: hs.Cwd, PermissionMode: hs.Mode, ServeRuntime: serveRuntimeFor(serve, store)}, store.Observe(meta.ID))
}

// spawnResumeWatcher starts the detached watcher for job. A seam, so tests can
// stand in for the process.
var spawnResumeWatcher = func(store *gatehandle.Store, j resumeJob) error {
	argv := []string{"hook", "resume", "--handle", j.Handle, "--harness", j.Harness, "--session", j.Session, "--owner", j.Owner}
	if j.Cwd != "" {
		argv = append(argv, "--cwd", j.Cwd)
	}
	if j.PermissionMode != "" {
		argv = append(argv, "--permission-mode", j.PermissionMode)
	}
	if j.ServeRuntime != "" {
		// The watcher is told both stores outright: it depends on neither the
		// environment it inherits nor the directory it starts in.
		argv = append(argv, "--serve-store", j.ServeRuntime, "--handle-store", store.RuntimeDir())
	}
	c, err := gateChildCommand(argv)
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(store.Dir(), j.Handle, "resume.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	c.Stdin, c.Stdout, c.Stderr = nil, logf, logf
	detachChild(c)
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

// resumeCommand builds the command that resumes the session; a seam so tests can
// run a stand-in for the harness binary and capture what it was given.
var resumeCommand = func(harness string, argv []string) *exec.Cmd {
	c := exec.Command(argv[0], argv[1:]...)
	c.Env = agentcli.ResumeEnv(harness, os.Environ())
	return c
}

func newHookResumeCommand() *cobra.Command {
	c := &cobra.Command{
		Use:    "resume",
		Hidden: true,
		Short:  "Wait for a gate handle, then resume its session with the verdict (started by the Stop hook)",
		Long: `resume is the watcher the Stop hook starts when a session's turn cannot carry a
gate verdict in-turn. It waits for the handle to finish, claims it once, and
resumes the same session with the verdict as the prompt. You do not run it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			j := resumeJob{}
			j.Handle, _ = f.GetString("handle")
			j.Harness, _ = f.GetString("harness")
			j.Session, _ = f.GetString("session")
			j.Owner, _ = f.GetString("owner")
			j.Cwd, _ = f.GetString("cwd")
			j.PermissionMode, _ = f.GetString("permission-mode")
			j.ServeRuntime, _ = f.GetString("serve-store")
			var store *gatehandle.Store
			if dir, _ := f.GetString("handle-store"); dir != "" {
				store = gatehandle.New(dir)
			} else {
				var ok bool
				if store, ok = hookGateStore(); !ok {
					return nil
				}
			}
			return runGateResume(store, j)
		},
	}
	for _, n := range []string{"handle", "harness", "session", "owner", "cwd", "permission-mode", "serve-store", "handle-store"} {
		c.Flags().String(n, "", "")
	}
	return c
}

// runGateResume waits for j.Handle to finish, then claims it — and any other
// handle of the session already armed for a resume — and resumes the session
// once with the verdicts as its prompt. A handle someone else already claimed is
// not delivered again.
//
// store is the handle plane — where the handle lives. The session's turn plane
// (is a turn open, who is resuming) is the store at j.ServeRuntime when the
// handle is in another repo's store, and the same store otherwise.
func runGateResume(store *gatehandle.Store, j resumeJob) error {
	serve := store
	if j.ServeRuntime != "" {
		serve = gatehandle.New(j.ServeRuntime)
	}
	res, ok := agentcli.StopResumeFor(j.Harness)
	if !ok {
		return fmt.Errorf("resume: %s", agentcli.StopResumeUnavailable(j.Harness))
	}
	deadline := time.Now().Add(gatehandle.MaxDeliveryAge)
	for !store.Observe(j.Handle).Terminal() {
		if store.Delivered(j.Handle) || !time.Now().Before(deadline) {
			return nil
		}
		time.Sleep(resumeWatchPoll)
	}
	// The verdict is held until the session is between turns: while the turn is
	// open and can still spend a continuation the Stop hook delivers it in-turn,
	// and a resume under a running turn would be a second writer on one session.
	for !serve.TurnIdle(j.Session, res.Cap, resumeQuiet) {
		if store.Delivered(j.Handle) || !time.Now().Before(deadline) {
			return nil
		}
		time.Sleep(resumeWatchPoll)
	}
	// One resume per session at a time: two gates finishing together must not
	// resume the session twice at once.
	unlock, ok := serve.LockSession(j.Session, gatehandle.MaxDeliveryAge)
	if !ok {
		return nil
	}
	defer unlock()

	var texts []string
	var claimed []gatehandle.Meta
	ids := []string{j.Handle}
	for _, id := range store.Undelivered() {
		if id != j.Handle && store.ResumeArmed(id) {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		m, err := store.Meta(id)
		if err != nil || !ownsGate(j.Owner, m) {
			continue
		}
		o := store.Observe(id)
		if o.Terminal() && store.Claim(id) {
			texts = append(texts, renderGateVerdict(o.Verdict))
			claimed = append(claimed, m)
		}
	}
	if len(texts) == 0 {
		return nil // another route delivered it
	}
	c := resumeCommand(j.Harness, res.Argv(j.Session, strings.Join(texts, "\n\n"), j.PermissionMode))
	if j.Cwd != "" {
		c.Dir = j.Cwd
	}
	c.Stdin, c.Stdout, c.Stderr = nil, os.Stdout, os.Stderr
	if err := c.Start(); err != nil {
		for _, m := range claimed {
			store.Release(m.ID) // not delivered: give it back to another route
		}
		return fmt.Errorf("resume: %s could not be started: %w", j.Harness, err)
	}
	recordGateDelivered(claimed)
	return c.Wait()
}
