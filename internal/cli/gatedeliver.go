package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/costview"
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
// so the hook answers rather than being killed.
const stopGateWaitDefault = 25 * time.Minute

// stopHookTimeoutSec is the timeout, in seconds, the scaffold writes on the Stop
// hook: longer than stopGateWaitDefault, so a wait ends in an answer.
const stopHookTimeoutSec = 30 * 60

func stopGateWait() time.Duration {
	if v := strings.TrimSpace(os.Getenv(stopGateWaitEnv)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
	}
	return stopGateWaitDefault
}

// hookGateStore resolves the handle store for the repo a hook fires in, the same
// way app.Open finds it but without opening the database — a hook must stay
// cheap. ok is false outside a governed repo, where a hook stays inert.
func hookGateStore() (*gatehandle.Store, bool) {
	cfg, cfgPath, err := config.Load("")
	if err != nil && !errors.Is(err, config.ErrNotFound) {
		return nil, false
	}
	root := "."
	if cfgPath != "" {
		root = config.RepoRootFromConfigPath(cfgPath)
	} else if cwd, e := os.Getwd(); e == nil {
		root = cwd
	}
	if _, ok := config.FindDataDir(root); !ok {
		return nil, false
	}
	return gatehandle.New(cfg.ResolveRuntimeDir(root).Dir), true
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
	var mine []string
	for _, id := range store.Undelivered() {
		if m, err := store.Meta(id); err == nil && ownsGate(session, m) {
			mine = append(mine, id)
		}
	}
	if len(mine) == 0 {
		return "", nil
	}
	if wait > 0 {
		waitForGates(store, mine, wait)
	}
	var blocks []string
	var delivered []gatehandle.Meta
	for _, id := range mine {
		v, ok := store.Load(id)
		if !ok || !store.Claim(id) {
			continue
		}
		blocks = append(blocks, renderGateVerdict(v))
		delivered = append(delivered, v.Meta)
	}
	return strings.Join(blocks, "\n\n"), delivered
}

// waitForGates blocks until every id is finished or died, or wait passes.
func waitForGates(store *gatehandle.Store, ids []string, wait time.Duration) {
	deadline := time.Now().Add(wait)
	for {
		running := false
		for _, id := range ids {
			if store.State(id) == gatehandle.Running {
				running = true
				break
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
// and deliver for this session. Fails open — no repo, no handles, no text.
func gateDeliveryFor(wait time.Duration) string {
	store, ok := hookGateStore()
	if !ok {
		return ""
	}
	text, delivered := deliverGates(store, config.ResolveSession(), wait)
	recordGateDelivered(delivered)
	return text
}

// recordGateDelivered writes each delivered run's delivery row (sty_c4b92c9e):
// the mark of when the harness handed the session its verdict, one of the rows a
// wait's model-call count is read from. It is a seam because the hook stays
// cheap and opens the store only when it has actually delivered something.
var recordGateDelivered = func(delivered []gatehandle.Meta) {
	var stories []gatehandle.Meta
	for _, m := range delivered {
		if m.Story != "" {
			stories = append(stories, m)
		}
	}
	if len(stories) == 0 {
		return
	}
	a, err := app.Open()
	if err != nil {
		return
	}
	defer a.Close()
	verb.SetLedgerStore(a.Store.Ledger)
	for _, m := range stories {
		verb.RecordGateWait(context.Background(), m.Story, costview.GatePhaseDelivered, m.ID, time.Now().UTC())
	}
}
