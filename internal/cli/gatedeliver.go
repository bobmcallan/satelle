package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/gatehandle"
	"github.com/bobmcallan/satelle/internal/verb"
)

// Delivery of a finished gate run into the driving session (sty_c4b92c9e).
//
// This is the only reader of a finished, undelivered handle, and it is reached
// only from a harness hook — never from a verb the driver could call. That is
// the point: the harness fires the hook, the verdict rides into the session as
// the hook's own output, and the driver never spends a model call asking. A verb
// that returned a handle's state would give the driver something to poll.
//
// Two hooks call it. The Stop hook is the wake: the driver ended its turn with a
// gate still running, so the hook waits for it (bounded by the hook's own
// timeout) and answers with a block whose reason is the verdict — which the
// harness feeds back as the session's next input. UserPromptSubmit is the
// catch-up: a gate that finished while the session was between turns is put in
// front of the model with the next prompt, without waiting.

// stopGateWaitEnv overrides how long the Stop hook waits for a running gate.
const stopGateWaitEnv = "SATELLE_GATE_STOP_WAIT"

// stopGateWaitDefault is how long the Stop hook waits for a running gate. It
// sits inside the Stop hook's own timeout in the scaffold (stopHookTimeoutSec),
// so the hook answers rather than being killed. A gate still running at the end
// of one wait is not released to idle: the hook blocks with a still-running note
// and the next Stop waits again.
const stopGateWaitDefault = 25 * time.Minute

// stopHookTimeoutSec is the timeout, in seconds, the scaffold writes on the Stop
// hook: longer than stopGateWaitDefault, so a wait ends in an answer.
const stopHookTimeoutSec = 30 * 60

// stopHookMargin is the room the hook keeps between the end of its wait and the
// harness's timeout on it, to render and write its answer.
const stopHookMargin = time.Minute

// stopGateWaitMax is the longest wait stopGateWait allows, whatever the
// override says: the Stop hook must answer before the harness kills it.
const stopGateWaitMax = stopHookTimeoutSec*time.Second - stopHookMargin

func stopGateWait() time.Duration {
	if v := strings.TrimSpace(os.Getenv(stopGateWaitEnv)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return min(d, stopGateWaitMax)
		}
	}
	return stopGateWaitDefault
}

// hookGateStore resolves the handle store for the repo a hook fires in, the same
// way app.Open finds it but without opening the database — a hook must stay
// cheap. ok is false outside a governed repo, where a hook stays inert.
func hookGateStore() (*gatehandle.Store, bool) {
	cfg, root, ok := firingRepo()
	if !ok {
		return nil, false
	}
	return gatehandle.New(cfg.ResolveRuntimeDir(root).Dir), true
}

// firingRepo is the repo a hook fires in: its config and root. ok is false
// outside a governed repo.
func firingRepo() (config.Config, string, bool) {
	cfg, cfgPath, err := config.Load("")
	if err != nil && !errors.Is(err, config.ErrNotFound) {
		return config.Config{}, "", false
	}
	root := "."
	if cfgPath != "" {
		root = config.RepoRootFromConfigPath(cfgPath)
	} else if cwd, e := os.Getwd(); e == nil {
		root = cwd
	}
	if _, ok := config.FindDataDir(root); !ok {
		return config.Config{}, "", false
	}
	return cfg, root, true
}

// ownsGate reports whether the session may be told about a run: its own, or one
// started where no session identity was resolvable on either side.
func ownsGate(session string, m gatehandle.Meta) bool {
	return session == "" || m.Session == "" || session == m.Session
}

// deliverFinishedGates hands the session every finished, undelivered gate run of
// its own, exactly once, and returns the text to put in front of it ("" when
// there is nothing to say). With wait > 0 it first waits — up to wait — for its
// running runs, which is what lets the Stop hook wake a session on completion.
func deliverFinishedGates(store *gatehandle.Store, session string, wait time.Duration) string {
	text, _ := deliverGates(store, session, wait)
	return text
}

// deliverGates is deliverFinishedGates that also names the runs it delivered,
// so the hook can record the delivery as a driver-usage row.
func deliverGates(store *gatehandle.Store, session string, wait time.Duration) (string, []gatehandle.Meta) {
	return deliverGatesMode(store, session, wait, false)
}

// deliverStopGates is deliverGates for the Stop hook, which never lets a session
// go idle on a gate that is still going: when no verdict is ready and a run of
// its own is still running, the text is a one-line still-running note instead,
// so the hook blocks the stop and the next Stop waits again. The loop ends when
// the verdict is delivered or the run is dead — a Died or Finished run is
// terminal, and a run whose liveness the platform cannot verify is noted once
// and then let go (see gatehandle.RunningUnverified).
func deliverStopGates(store *gatehandle.Store, session string, wait time.Duration) (string, []gatehandle.Meta) {
	return deliverGatesMode(store, session, wait, true)
}

func deliverGatesMode(store *gatehandle.Store, session string, wait time.Duration, stopWake bool) (string, []gatehandle.Meta) {
	return deliverRefs(ownedGates([]handleStore{{store: store}}, session), wait, stopWake)
}

// deliverRefs is the delivery over runs that may live in different stores: each
// is observed, claimed and marked in the store that holds it.
func deliverRefs(mine []gateRef, wait time.Duration, stopWake bool) (string, []gatehandle.Meta) {
	if len(mine) == 0 {
		return "", nil
	}
	if wait > 0 {
		waitForGates(mine, wait)
	}
	// One look per run, and everything is decided from it: a run that finishes
	// between two looks would otherwise be "running" to the first and "done" to
	// the second, and the stop would be let through with its verdict undelivered.
	var blocks, notes []string
	var delivered []gatehandle.Meta
	var toMark []stillRunning
	for _, g := range mine {
		o := g.store.Observe(g.id)
		switch {
		case o.Terminal():
			if g.store.Claim(g.id) {
				blocks = append(blocks, renderGateVerdict(o.Verdict))
				delivered = append(delivered, o.Verdict.Meta)
			}
		case stopWake && o.State == gatehandle.Running:
			toMark = append(toMark, stillRunning{gateRef: g})
			notes = append(notes, stillRunningNote(g.store, g.id, ""))
		case stopWake && o.State == gatehandle.RunningUnverified && !g.store.UnverifiedNotified(g.id):
			toMark = append(toMark, stillRunning{gateRef: g, unverified: true, reason: o.Reason})
			notes = append(notes, stillRunningNote(g.store, g.id, o.Reason))
		}
	}
	if len(blocks) > 0 {
		return strings.Join(blocks, "\n\n"), delivered
	}
	for _, r := range toMark {
		r.store.MarkNotified(r.id, r.unverified, r.reason)
	}
	return strings.Join(notes, "\n"), nil
}

// stillRunning is a run the Stop hook is about to tell its session about.
type stillRunning struct {
	gateRef
	unverified bool
	reason     string
}

// stillRunningNote is the one line a session is handed while its gate is still
// going: which run, for how long, and what to do — end the turn, never poll,
// because the next Stop waits again and delivers the verdict.
func stillRunningNote(store *gatehandle.Store, id, unverifiedReason string) string {
	m, _ := store.Meta(id)
	elapsed := time.Since(m.Started).Round(time.Second)
	what := fmt.Sprintf("`satelle %s` for %s", strings.Join(m.Argv, " "), storyLabel(m))
	if unverifiedReason != "" {
		return fmt.Sprintf("satelle: gate %s — %s — has run %s and this platform cannot verify it is still going (%s); its verdict is delivered with your next prompt or stop once it finishes. End your turn; do not poll.", id, what, elapsed, unverifiedReason)
	}
	return fmt.Sprintf("satelle: gate %s — %s — is still running after %s. End your turn to keep waiting; do not poll.", id, what, elapsed)
}

// waitForGates blocks until one of refs has finished (a Finished or Died run is a
// verdict ready to hand over), or until none of them is worth holding the
// session for — each is no longer running, or is a run whose liveness the
// session was already told it cannot verify — or wait passes. A verdict that is
// ready is delivered at once and never held behind a slower gate: the rest are
// waited for by the next Stop (sty_7e4393fc).
func waitForGates(refs []gateRef, wait time.Duration) {
	deadline := time.Now().Add(wait)
	for {
		running := false
		for _, g := range refs {
			switch g.store.State(g.id) {
			case gatehandle.Finished, gatehandle.Died:
				return
			case gatehandle.Running:
				running = true
			case gatehandle.RunningUnverified:
				running = running || !g.store.UnverifiedNotified(g.id)
			}
		}
		if !running || !time.Now().Before(deadline) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// renderGateVerdict is the block a session is handed for one finished run: the
// handle and the command it ran, how it ended, and the verdict — what each gate
// decided. Nothing else: not the record the command printed (a whole story, for
// a status change) and not the advisory notes it wrote to stderr, which a
// session pays to read and cannot act on.
func renderGateVerdict(v gatehandle.Verdict) string {
	how := "completed"
	switch {
	case v.Died:
		how = "DIED before recording a result"
	case v.Result.ExitCode != 0:
		how = "FAILED (rejected or errored)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "satelle: gate %s finished — `satelle %s` for %s %s.", v.Meta.ID, strings.Join(v.Meta.Argv, " "), storyLabel(v.Meta), how)
	if out := verdictBlock(v); out != "" {
		b.WriteString("\n" + out)
	}
	return b.String()
}

// verdictBlock is the verdict of one finished run and nothing but it: each
// gate's decision line, the error that ended a failed run (a reject arrives as
// one, carrying the reviewer's notes), and — for a create — the id it made,
// which is the one product of the run the driver still needs. It is what an
// agent-facing call returns inline and what a hook delivers later, so the two
// read the same.
func verdictBlock(v gatehandle.Verdict) string {
	var parts []string
	if lines := strings.TrimSpace(v.Lines); lines != "" {
		parts = append(parts, lines)
	}
	if v.Result.Error != "" {
		parts = append(parts, v.Result.Error)
	}
	if id := createdID(v); id != "" {
		parts = append(parts, "created: "+id)
	}
	return strings.Join(parts, "\n")
}

// createdID reads the id a create verb printed. Only a create has a product the
// session cannot get back from the store by name; every other verb's stdout is
// the record the session already holds.
func createdID(v gatehandle.Verdict) string {
	if !strings.HasSuffix(v.Meta.Verb, "-create") || v.Result.ExitCode != 0 {
		return ""
	}
	var rec struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(v.Stdout)), &rec) != nil {
		return ""
	}
	return rec.ID
}

func storyLabel(m gatehandle.Meta) string {
	if m.Story != "" {
		return m.Story
	}
	return m.Verb
}

// gateDeliveryFor is the hook-side entry point: resolve the repo's handle store
// and deliver for this session — from that store and from any store it points at
// for the session (handleStoresFor). Fails open — no repo, no handles, no text.
func gateDeliveryFor(wait time.Duration) string {
	return hookGateDelivery(wait, false)
}

// stopGateDeliveryFor is gateDeliveryFor for the Stop hook: it also answers with
// a still-running note while a gate the session handed off is still going.
func stopGateDeliveryFor(wait time.Duration) string {
	return hookGateDelivery(wait, true)
}

func hookGateDelivery(wait time.Duration, stopWake bool) string {
	home, ok := hookGateStore()
	if !ok {
		return ""
	}
	session := config.ResolveSession()
	text, delivered := deliverRefs(ownedGates(handleStoresFor(home, session), session), wait, stopWake)
	recordGateDelivered(delivered)
	return text
}

// recordGateDelivered writes each delivered run's delivery row (sty_c4b92c9e):
// the mark of when the harness handed the session its verdict, one of the rows a
// wait's model-call count is read from. It is a seam because the hook stays
// cheap and opens the store only when it has actually delivered something. The
// row goes to the ledger of the repo the run's story lives in — for a run started
// across repos, the one holding the handle, not the one whose hook delivered it.
var recordGateDelivered = func(delivered []gatehandle.Meta) {
	byRepo := map[string][]gatehandle.Meta{}
	for _, m := range delivered {
		if m.Story != "" {
			byRepo[m.Repo] = append(byRepo[m.Repo], m)
		}
	}
	for repo, stories := range byRepo {
		if repo != "" {
			recordDeliveredIn(repo, stories)
			continue
		}
		a, err := app.Open() // the repo this hook fires in
		if err != nil {
			continue
		}
		verb.SetLedgerStore(a.Store.Ledger)
		recordDeliveryRows(stories)
		a.Close()
	}
}
