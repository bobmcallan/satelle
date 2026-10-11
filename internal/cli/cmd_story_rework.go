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
	"github.com/bobmcallan/satelle/internal/placement"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func storyReworkCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rework <id>",
		Short: "Run the step's bounded rework relay — a coder session and a consulting reviewer session",
		Long: `Relay the step's performer binding and its consult binding as two live
sessions until the consultant says ready or the round budget is spent. The step
opts in: rework = { consult = "reviewer", rounds = 3 }.

An unanswered directed message to the performer (addressed to its binding by
name, no later reply from it) opens the relay performer-first, and READY cannot
end the relay while it stands.

The consultant's reply must END with a line exactly READY or NOT READY: <reason>;
anything else consumes a round. --rounds may only LOWER the authored budget.

Turns are ledgered as agent_message rows (cc="*"), so satelle story messages <id>
reads as the conversation. Prints {converged, rounds, last_objection}.

Does NOT change status: ready is a signal, never a verdict.
See satelle help agent-dispatch.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE:        runStoryRework,
	}
	cmd.Flags().Int("rounds", 0, "lower the step's authored round budget for this run (never raises it)")
	cmd.Flags().String("model", "", "model for the coder session, overriding agents.toml (recorded with source=agent)")
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

// refuseRemoteRework refuses a relay for a child the placement rule places
// remote (step.toml remote_agent, sty_dde8b6a4). A cloud session is one-shot and
// cannot be a relay partner, so running the relay would put remote work under a
// local coder unannounced. The way forward is the edge itself: re-presenting it
// launches a fresh cloud session carrying the reviewers' findings. The refusal
// is ledgered. A child placed local — including the not-signed-in fallback —
// passes: its performer really is local.
func refuseRemoteRework(ctx context.Context, d wfgovern.DerivedRoute, it workitem.Item) error {
	step, ok := d.Spec.StateNamed(it.Status)
	if !ok {
		return nil
	}
	placed := placement.Decide(ctx, it, step)
	if !placed.Remote {
		return nil
	}
	err := fmt.Errorf(
		"satelle story rework: %s is placed remote (remote_agent %q on step %q) — a cloud session cannot be a relay partner; re-present the edge instead to dispatch a fresh cloud session with the reviewers' findings",
		it.ID, placed.Agent, it.Status)
	_ = verb.AppendTelemetry(ctx, it.ID, "orchestrator", "rework_refused", map[string]any{
		"placement": placement.Remote, "step": it.Status, "agent": placed.Agent, "reason": err.Error(),
	})
	return err
}

func runStoryRework(cmd *cobra.Command, args []string) error {
	id := strings.TrimSpace(args[0])
	// The relay runs a consulting reviewer for minutes: an agent-facing call is
	// handed to a detached run like any other gate (sty_c4b92c9e).
	if handled, herr := handOffGate(cmd, "story-rework", id); handled {
		return herr
	}
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
	if err := refuseRemoteRework(ctx, d, it); err != nil {
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
	consultBinding, found := eff.Agents.NamedBinding(rw.ConsultBinding)
	if !found {
		return fmt.Errorf("satelle story rework: no [%s] binding in .satelle/workflows/agents.toml — the step allocates it as the consultant", rw.ConsultBinding)
	}
	// Idle-stall bound for each side's turns (sty_752c4ef2), resolved the same
	// way a one-shot dispatch resolves it: binding idle_timeout= over the
	// shared [defaults] table over the shipped default.
	coderIdle, ierr := eff.Agents.ResolveIdleTimeout(coderBinding, agentstep.DefaultIdleTimeout)
	if ierr != nil {
		return fmt.Errorf("satelle story rework: invalid idle_timeout in .satelle/workflows/agents.toml [%s]: %w", rw.CoderBinding, ierr)
	}
	consultIdle, ierr := eff.Agents.ResolveIdleTimeout(consultBinding, agentstep.DefaultIdleTimeout)
	if ierr != nil {
		return fmt.Errorf("satelle story rework: invalid idle_timeout in .satelle/workflows/agents.toml [%s]: %w", rw.ConsultBinding, ierr)
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
	// A long relay round between edits fires no hook, so nothing else refreshes
	// the seat's heartbeat while it waits on a model (sty_7f3e6fd3).
	if leaseFound {
		defer lease.KeepAlive(ctx, a.Store.Leases, it.ID, lease.ResolveOwner())()
	}

	out := cmd.OutOrStdout()
	coderLedger := &storeSessionLedger{ctx: ctx, storyID: it.ID, ls: a.Store.Ledger, actor: rw.CoderBinding}
	consultLedger := &storeSessionLedger{ctx: ctx, storyID: it.ID, ls: a.Store.Ledger, actor: rw.ConsultBinding}

	// The coder DRIVES its own edits (executor charter, own grant, seat-checked);
	// the consultant is ASKED (consult charter, mutators refused outright). The
	// role is stated, not inferred from the binding name — a repo may name its
	// coder anything (sty_8e0b29a0). The relay marker rides only the coder
	// spawn: ACP/stream snapshot os.Environ at open, and the consultant must
	// not inherit a marker that names the coder binding.
	modelFlag, _ := cmd.Flags().GetString("model")

	var coder agentcli.Session
	err = withRelayMarker(rw.CoderBinding, it.ID, func() error {
		var openErr error
		coder, openErr = reworkSessionOpener(ctx, eng, rw.CoderBinding, agentstep.SessionRoleDriving, it,
			reworkCoderPolicy(rw.CoderBinding, coderBinding.Tools, seat, invocationRecorder(coderLedger)),
			reworkEventHandler(coderLedger), modelFlag)
		return openErr
	})
	if err != nil {
		return err
	}
	defer func() { _ = coder.Close() }()

	consultant, err := reworkSessionOpener(ctx, eng, rw.ConsultBinding, agentstep.SessionRoleConsult, it,
		reworkConsultPolicy(invocationRecorder(consultLedger)),
		reworkEventHandler(consultLedger), "")
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
	var pending []string
	for _, m := range unansweredDirected(verb.EngagementMessages(ctx, it.ID), rw.CoderBinding) {
		pending = append(pending, fmt.Sprintf("from %s: %s", m.From, strings.TrimSpace(m.Body)))
	}
	loop := &reworkLoop{
		Coder: coder, Consultant: consultant,
		CoderRole: rw.CoderBinding, ConsultRole: rw.ConsultBinding,
		Rounds:             rw.Rounds,
		Ledger:             consultLedger,
		Out:                out,
		Seed:               consultPayload + "\n\n" + reworkSeed(rw),
		CoderSeed:          coderPayload + "\n\n" + reworkCoderSeed(rw),
		Pending:            pending,
		CoderIdleTimeout:   coderIdle,
		ConsultIdleTimeout: consultIdle,
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

// unansweredDirected returns the messages addressed to coder by name that it has
// not answered, oldest first (msgs is oldest first). A later message FROM coder
// answers everything before it, whoever it was addressed to — msgs must be the
// unfiltered window (verb.EngagementMessages), not one filtered to the coder's
// inbox. Broadcasts do not count, and neither do the
// relay's own transcript rows (cc="*"): those are conversation, not a directive,
// and a relay that ended on the consultant's READY must not make the next one
// start coder-first.
func unansweredDirected(msgs []verb.AgentMessage, coder string) []verb.AgentMessage {
	var out []verb.AgentMessage
	for _, m := range msgs {
		switch {
		case m.From == coder:
			out = nil
		case m.To == coder && m.Cc != "*":
			out = append(out, m)
		}
	}
	return out
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

// invocationRecorder adapts a sessionLedger to the permission-decision callback,
// so a relay's allow/deny rows land beside the chat loop's in the same shape.
func invocationRecorder(l sessionLedger) func(agentcli.PermissionRequest, bool, string) {
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
func reworkEventHandler(l sessionLedger) agentcli.EventHandler {
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
// modelOverride is the --model flag value for THIS open (sty_7069bced) —
// recorded with source=agent when non-empty. Only the coder open receives it;
// the consultant always opens with an empty override.
var reworkSessionOpener = func(
	ctx context.Context,
	eng *agentstep.Engine,
	binding string,
	role agentstep.SessionRole,
	it workitem.Item,
	pol agentcli.PermissionPolicy,
	onEvent agentcli.EventHandler,
	modelOverride string,
) (agentcli.Session, error) {
	return eng.OpenSessionAsWithModel(ctx, binding, role, it, pol, onEvent, modelOverride)
}
