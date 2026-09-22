package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func storyReworkCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rework <id>",
		Short: "Run the step's bounded rework relay — a coder session and a consulting reviewer session",
		Long: `Relay the current step's performer binding and its consult binding as two live
sessions until the consultant says ready or the round budget is spent. The step
opts in: rework = { consult = "reviewer", rounds = 3 }.

Termination contract: the consultant's reply must END with a line that is
exactly READY or NOT READY: <reason>. Anything else counts as not ready and
consumes a round. --rounds may only LOWER the authored budget.

Turns are ledgered as agent_message rows with real from/to roles and cc="*", so
satelle story messages <id> reads as the conversation and the edge reviewer
gets the transcript. Prints {converged, rounds, last_objection}.

Does NOT change status: ready is a signal, never a verdict — the entry gate
still decides, cold and one-shot.

See satelle help agent-dispatch.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE:        runStoryRework,
	}
	cmd.Flags().Int("rounds", 0, "lower the step's authored round budget for this run (never raises it)")
	return cmd
}

// reworkPlan is what the route says about this story's rework loop: who codes,
// who consults, and how many rounds. Resolved once so the command reads as the
// three questions it asks.
type reworkPlan struct {
	CoderBinding   string
	ConsultBinding string
	Rounds         int
}

// resolveReworkPlan reads the loop off the governing route for the story's
// CURRENT status. It refuses rather than guesses: a step with no rework key has
// no loop, and inventing one would be the binary deciding process.
func resolveReworkPlan(d wfgovern.DerivedRoute, status string) (reworkPlan, error) {
	var rw reworkPlan
	for _, w := range d.Reworks {
		if w.Step == status {
			rw.ConsultBinding, rw.Rounds = w.Consult, w.Rounds
			break
		}
	}
	if rw.ConsultBinding == "" {
		return reworkPlan{}, fmt.Errorf(
			"satelle story rework: step %q declares no rework loop — author rework = { consult = \"<binding>\", rounds = N } on that step in .satelle/workflows/step.toml",
			status)
	}
	agent, known := d.Spec.StateAgent(status)
	if !known || strings.TrimSpace(agent) == "" {
		return reworkPlan{}, fmt.Errorf(
			"satelle story rework: step %q allocates no performer — there is nobody to code with", status)
	}
	rw.CoderBinding = agent
	return rw, nil
}

func runStoryRework(cmd *cobra.Command, args []string) error {
	id := strings.TrimSpace(args[0])
	eng, a, err := engineForCmd(cmd)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	it, err := a.Store.Stories.Get(ctx, id)
	if err != nil {
		return err
	}
	if it.Kind != workitem.KindStory {
		return fmt.Errorf("satelle story rework: %s is not a story", id)
	}
	wfs, err := a.Store.DocIndex.List(ctx, "workflows")
	if err != nil {
		return err
	}
	d, _, err := wfgovern.RouteFor(wfs, it)
	if err != nil {
		return err
	}
	rw, err := resolveReworkPlan(d, it.Status)
	if err != nil {
		return err
	}
	// The budget is CONFIGURATION. --rounds is an operator saying "spend less
	// today", never "spend more than the route allows".
	if n, _ := cmd.Flags().GetInt("rounds"); n > 0 {
		if n > rw.Rounds {
			return fmt.Errorf("satelle story rework: --rounds %d exceeds the step's authored budget of %d — the budget is configuration; lower it in step.toml to raise it here", n, rw.Rounds)
		}
		rw.Rounds = n
	}
	eff, err := requireAgents(a)
	if err != nil {
		return err
	}
	coderBinding, found := eff.Agents.NamedBinding(rw.CoderBinding)
	if !found {
		return fmt.Errorf("satelle story rework: no [%s] binding in .satelle/workflows/agents.toml — the step allocates it as the performer", rw.CoderBinding)
	}

	// Adopt the lease's stamped session so the coder's hook and the relay
	// policy resolve the same live seat (sty_7567f047). Set only on this
	// process env — do not PublishSession an adopted lease id, or the parent
	// shell's later CLI calls would re-bind to it.
	fallbackSID := config.ResolveSession()
	var leaseRow lease.Lease
	leaseFound := false
	if a.Store.Leases != nil {
		if l, lerr := a.Store.Leases.Get(ctx, it.ID); lerr == nil {
			leaseRow, leaseFound = l, true
		} else if !errors.Is(lerr, lease.ErrNotFound) {
			return fmt.Errorf("satelle story rework: lease lookup: %w", lerr)
		}
	}
	sid := reworkSessionID(leaseRow, leaseFound, fallbackSID)
	if sid != "" {
		_ = os.Setenv(config.SessionEnv, sid)
		if sid == fallbackSID {
			config.PublishSession(sid)
		}
	}
	seat := func() (seatInfo, bool, error) { return resolveSeat(true, sid) }

	out := cmd.OutOrStdout()
	coderLedger := &storeChatLedger{ctx: ctx, storyID: it.ID, ls: a.Store.Ledger, actor: rw.CoderBinding}
	consultLedger := &storeChatLedger{ctx: ctx, storyID: it.ID, ls: a.Store.Ledger, actor: rw.ConsultBinding}

	// The coder DRIVES its own edits (executor charter, own grant, seat-checked);
	// the consultant is ASKED (consult charter, mutators refused outright). The
	// role is stated, not inferred from the binding name — a repo may name its
	// coder anything (sty_8e0b29a0). The relay marker rides only the coder
	// spawn: ACP/stream snapshot os.Environ at open, and the consultant must
	// not inherit a marker that names the coder binding.
	var coder agentcli.Session
	err = withRelayMarker(rw.CoderBinding, it.ID, func() error {
		var openErr error
		coder, openErr = reworkSessionOpener(ctx, eng, rw.CoderBinding, agentstep.SessionRoleDriving, it,
			reworkCoderPolicy(rw.CoderBinding, coderBinding.Tools, seat, invocationRecorder(coderLedger)),
			reworkEventHandler(coderLedger))
		return openErr
	})
	if err != nil {
		return err
	}
	defer func() { _ = coder.Close() }()

	consultant, err := reworkSessionOpener(ctx, eng, rw.ConsultBinding, agentstep.SessionRoleConsult, it,
		reworkConsultPolicy(invocationRecorder(consultLedger)),
		reworkEventHandler(consultLedger))
	if err != nil {
		return err
	}
	defer func() { _ = consultant.Close() }()

	fmt.Fprintf(out, "satelle story rework %s  (status %s; %s ↔ %s; up to %d round(s))\n",
		it.ID, it.Status, rw.CoderBinding, rw.ConsultBinding, rw.Rounds)

	// Both sides are seeded with the story payload — the ACs are the criteria
	// for coder, consultant and gate alike, and a live session's command
	// template need not carry {payload}.
	consultPayload, err := eng.SessionSeed(ctx, it, rw.ConsultBinding)
	if err != nil {
		return err
	}
	coderPayload, err := eng.SessionSeed(ctx, it, rw.CoderBinding)
	if err != nil {
		return err
	}
	loop := &reworkLoop{
		Coder: coder, Consultant: consultant,
		CoderRole: rw.CoderBinding, ConsultRole: rw.ConsultBinding,
		Rounds:    rw.Rounds,
		Ledger:    consultLedger,
		Out:       out,
		Seed:      consultPayload + "\n\n" + reworkSeed(rw),
		CoderSeed: coderPayload + "\n\n" + reworkCoderSeed(rw),
	}
	res, runErr := loop.Run(ctx)
	// The RESULT is recorded whether or not the relay finished cleanly: a relay
	// that died on round two still cost two rounds, and the orchestrator needs
	// to see that rather than infer it.
	recordReworkResult(ctx, a.Store.Ledger, it.ID, rw, res)
	body, mErr := json.Marshal(res)
	if mErr != nil {
		return mErr
	}
	fmt.Fprintf(out, "\n%s\n", body)
	if runErr != nil {
		_ = coder.Cancel()
		_ = consultant.Cancel()
		return runErr
	}
	return nil
}

// reworkSeed is the consultant's first turn: the ask and the termination
// contract. The CONTRACT lives in prose handed to the agent, not in a Go
// branch — what Go owns is parsing the marker back (parseReadyMarker).
func reworkSeed(rw reworkPlan) string {
	return fmt.Sprintf(`Review the slice as it currently stands in the working tree against this story's acceptance criteria.

You are in a bounded REWORK RELAY with the %q session, which will fix what you raise. The budget is %d round(s); this is round one.

Answer with your findings, then end your reply with a FINAL LINE that is exactly one of:

  READY
  NOT READY: <the single most important thing still wrong>

The final line is a contract, not a formality: satelle reads it to decide whether to stop relaying. Anything else counts as NOT READY and spends a round.

READY is not a verdict and does not advance anything. The reviewer that gates this edge runs cold and one-shot afterwards; your reply is context for it. Do not run satelle story set and do not change this story's status.`,
		rw.CoderBinding, rw.Rounds)
}

// reworkCoderSeed is the coder's first-turn briefing: what the relay is, and
// the one thing it must not do. Its charter is the executor's — it edits under
// its own grant inside the seat — so this says only what the relay adds.
func reworkCoderSeed(rw reworkPlan) string {
	return fmt.Sprintf(`You are in a bounded REWORK RELAY with the %q session, which is reviewing your slice against this story's acceptance criteria. The budget is %d round(s).

Fix what it raises, in the working tree, then reply with what you changed and why — that reply is relayed straight back to it for re-check. Stay inside the story's acceptance criteria; do not widen the slice.

Do NOT change this story's status: the relay does not advance anything. The edge is presented by the orchestrator afterwards and judged by a cold gate.`,
		rw.ConsultBinding, rw.Rounds)
}

// recordReworkResult appends the relay's outcome as a ledger row so the
// orchestrator can read it from the ledger as well as from stdout. Best-effort:
// a ledger write failure must not turn a completed relay into a command error.
func recordReworkResult(ctx context.Context, ls *ledger.Store, storyID string, rw reworkPlan, res reworkResult) {
	if ls == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"converged": res.Converged, "rounds": res.Rounds, "last_objection": res.LastObjection,
		"coder": rw.CoderBinding, "consult": rw.ConsultBinding, "budget": rw.Rounds,
	})
	if err != nil {
		return
	}
	_, _ = ls.Append(ctx, ledger.AppendInput{
		StoryID: storyID,
		Kind:    ledger.KindAgentInvocation,
		Actor:   rw.ConsultBinding,
		Body:    fmt.Sprintf("rework converged=%t rounds=%d/%d", res.Converged, res.Rounds, rw.Rounds),
		Payload: payload,
	}, time.Now())
}

// invocationRecorder adapts a chatLedger to the permission-decision callback,
// so a relay's allow/deny rows land beside the chat loop's in the same shape.
func invocationRecorder(l chatLedger) func(agentcli.PermissionRequest, bool, string) {
	return func(req agentcli.PermissionRequest, allow bool, by string) {
		if l == nil {
			return
		}
		dec := "deny"
		if allow {
			dec = "allow"
		}
		_ = l.WriteInvocation(req.ToolName, req.Kind, dec, by)
	}
}

// reworkEventHandler ledgers a session's tool boundaries. Installed as the
// request's OnEvent for the same reason the chat loop does it there: transports
// call it inline before the lossy Events() fan-out, so no boundary is lost.
func reworkEventHandler(l chatLedger) agentcli.EventHandler {
	return func(ev agentcli.Event) {
		if l == nil {
			return
		}
		switch ev.Kind {
		case agentcli.EventToolStart:
			_ = l.WriteInvocation(ev.Tool, ev.Status, "start", "session")
		case agentcli.EventToolEnd:
			_ = l.WriteInvocation(ev.Tool, ev.Status, "end", "session")
		}
	}
}

// reworkSessionID returns the session identity the rework relay exports into
// its children. A stamped lease wins so pickSessionSeat binds the seat the
// relay is driving; an unstamped or absent lease keeps today's ResolveSession
// fallback (tree-routing still works).
func reworkSessionID(l lease.Lease, found bool, fallback string) string {
	if found {
		if id := strings.TrimSpace(l.SessionID); id != "" {
			return id
		}
	}
	return strings.TrimSpace(fallback)
}

// withRelayMarker sets SATELLE_RELAY_BINDING / SATELLE_RELAY_ITEM on the process
// environment for the duration of fn, then clears both. The rework verb wraps
// only the coder OpenSessionAs in this helper so the consultant's env snapshot
// has no marker (ACP and stream compose os.Environ at open time).
func withRelayMarker(binding, item string, fn func() error) error {
	prevBinding, hadBinding := os.LookupEnv(config.RelayBindingEnv)
	prevItem, hadItem := os.LookupEnv(config.RelayItemEnv)
	_ = os.Setenv(config.RelayBindingEnv, binding)
	_ = os.Setenv(config.RelayItemEnv, item)
	defer func() {
		if hadBinding {
			_ = os.Setenv(config.RelayBindingEnv, prevBinding)
		} else {
			_ = os.Unsetenv(config.RelayBindingEnv)
		}
		if hadItem {
			_ = os.Setenv(config.RelayItemEnv, prevItem)
		} else {
			_ = os.Unsetenv(config.RelayItemEnv)
		}
	}()
	return fn()
}

// reworkSessionOpener opens a live session for the rework relay. Tests may
// replace it to observe the process env at each open (sty_7567f047 AC3).
var reworkSessionOpener = func(
	ctx context.Context,
	eng *agentstep.Engine,
	binding string,
	role agentstep.SessionRole,
	it workitem.Item,
	pol agentcli.PermissionPolicy,
	onEvent agentcli.EventHandler,
) (agentcli.Session, error) {
	return eng.OpenSessionAs(ctx, binding, role, it, pol, onEvent)
}
