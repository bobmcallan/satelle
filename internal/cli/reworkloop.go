// The rework relay's transport-free core (sty_8e0b29a0).
//
// Deliberately a SIBLING of chatloop.go rather than an extension of it. chat is
// human↔agent: an interactive scanner, a prompt, an inbox watermark, a human
// permission ask. The relay is agent↔agent and headless: no reader, no prompt,
// a fixed turn protocol and a termination rule. What the two genuinely share —
// accumulating one turn's reply off a session — is shared as drainReply; what
// they do not share stays apart, because folding prompt semantics into a
// headless loop is how both stop being readable.
//
// The relay decides TERMINATION (a rule: the marker, or the budget). It never
// decides a verdict and never moves status: the orchestrator presents the edge
// and the edge's reviewer runs cold over the payload, exactly as
// [[satelle-agent-consultation]] reserves.
package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// Ready markers. The CONTRACT is stated to the consultant in the relay's seed
// text (substrate-adjacent prose); what lives in Go is only its PARSING, the
// same split the gate rubrics keep.
const (
	readyMarker    = "READY"
	notReadyMarker = "NOT READY:"
)

// reworkResult is the relay's whole answer: did the consultant say ready, how
// many rounds it cost, and the last objection if it did not. A signal for the
// orchestrator, never a verdict.
type reworkResult struct {
	Converged     bool   `json:"converged"`
	Rounds        int    `json:"rounds"`
	LastObjection string `json:"last_objection,omitempty"`
}

// reworkLoop relays turns between a coder session and a consulting reviewer
// session on one story. Roles are the BINDING NAMES, which is what the ledger
// rows carry, so `satelle story messages` reads as the conversation it was.
type reworkLoop struct {
	Coder, Consultant      agentcli.Session
	CoderRole, ConsultRole string
	// Rounds is the budget: the maximum number of consultant→coder exchanges.
	Rounds int
	Ledger chatLedger
	Out    io.Writer
	// Seed is the first turn handed to the consultant — the story payload, the
	// ask, and the marker contract. CoderSeed is prepended to the coder's FIRST
	// turn for the same reason: both sides must have seen the acceptance
	// criteria, since the transcript is context and the ACs are the criteria.
	// Both are supplied by the command so the prose stays out of the loop.
	Seed      string
	CoderSeed string
	// CoderIdleTimeout / ConsultIdleTimeout bound one turn on their respective
	// session with a Watchdog (sty_752c4ef2) — each side may configure its own
	// idle_timeout=. ≤0 disables stall detection for that side.
	CoderIdleTimeout, ConsultIdleTimeout time.Duration
}

// Run drives the relay. The consultant speaks first (it reviews the slice as it
// stands), then each round is consultant→coder→consultant. It returns as soon
// as the consultant emits READY, or when the budget is spent.
//
// Every turn is ledgered with its real from/to roles AND cc="*": the direction
// is the conversation's, the audience is whoever later judges this edge.
func (l *reworkLoop) Run(ctx context.Context) (reworkResult, error) {
	if l.Out == nil {
		l.Out = io.Discard
	}
	if l.Rounds <= 0 {
		return reworkResult{}, fmt.Errorf("rework: round budget must be > 0")
	}
	res := reworkResult{}
	turn := l.Seed
	for round := 1; round <= l.Rounds; round++ {
		fmt.Fprintf(l.Out, "\n— round %d/%d: %s —\n", round, l.Rounds, l.ConsultRole)
		verdict, err := l.exchange(ctx, l.Consultant, l.ConsultRole, l.CoderRole, turn, l.ConsultIdleTimeout)
		if err != nil {
			return res, err
		}
		res.Rounds = round
		ready, objection := parseReadyMarker(verdict)
		if ready {
			res.Converged = true
			res.LastObjection = ""
			return res, nil
		}
		res.LastObjection = objection

		fmt.Fprintf(l.Out, "\n— round %d/%d: %s —\n", round, l.Rounds, l.CoderRole)
		ask := objection
		if round == 1 && strings.TrimSpace(l.CoderSeed) != "" {
			ask = l.CoderSeed + "\n\n" + objection
		}
		reply, err := l.exchange(ctx, l.Coder, l.CoderRole, l.ConsultRole, ask, l.CoderIdleTimeout)
		if err != nil {
			return res, err
		}
		turn = reply
	}
	return res, nil
}

// exchange sends one turn to a session, ledgers the reply under its real
// direction, and returns it. An empty reply is not an error — the budget still
// advanced, and the caller treats it as the contract violation it is.
func (l *reworkLoop) exchange(ctx context.Context, sess agentcli.Session, from, to, text string, idle time.Duration) (string, error) {
	if strings.TrimSpace(text) == "" {
		text = "(no message)"
	}
	if err := sess.Send(ctx, agentcli.Turn{Text: text}); err != nil {
		return "", err
	}
	rendered, err := drainReply(ctx, sess, l.Out, from, idle)
	reply := turnResult(sess, rendered)
	if err != nil {
		return reply, err
	}
	if strings.TrimSpace(reply) != "" && l.Ledger != nil {
		_ = l.Ledger.WriteMessage(from, to, "*", reply)
	}
	return reply, nil
}

// turnResult is the VERBATIM text of the turn that just completed.
//
// The event stream is a RENDER channel: both transports pass assistant text
// through agentcli.SafeText, which flattens whitespace and caps at 240 runes.
// That is right for a terminal and wrong for both of this relay's consumers —
// a marker contract on the reply's final LINE cannot survive flattening, and a
// truncated transcript is a worse gate payload than a whole one. Captured() is
// the transport's per-turn result, unflattened and uncapped, so it is the
// source of truth; the rendered text is the fallback for a transport that
// captures nothing (sty_8e0b29a0).
func turnResult(sess agentcli.Session, rendered string) string {
	if b := sess.Captured(); len(strings.TrimSpace(string(b))) > 0 {
		return string(b)
	}
	return rendered
}

// parseReadyMarker reads the termination contract off a consultant's reply: its
// FINAL non-empty line is `READY` or `NOT READY: <reason>`.
//
// An unparseable reply is NOT READY with the whole trimmed reply as the
// objection, and it consumes a round. That is deliberate: a consultant that
// cannot follow the contract must not be able to hang the loop, and the honest
// reading of "it did not say ready" is that it is not ready.
func parseReadyMarker(reply string) (ready bool, objection string) {
	last := ""
	for _, line := range strings.Split(reply, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			last = t
		}
	}
	switch {
	case last == readyMarker:
		return true, ""
	case strings.HasPrefix(last, notReadyMarker):
		reason := strings.TrimSpace(strings.TrimPrefix(last, notReadyMarker))
		if reason == "" {
			reason = "(no reason given)"
		}
		return false, reason
	}
	if t := strings.TrimSpace(reply); t != "" {
		return false, t
	}
	return false, "consultant returned no reply"
}

// reworkCoderPolicy is the coder side's permission gate: a mutator passes only
// when the coder's OWN grant admits mutators AND the seat is live in a
// performing state the route allocates to that binding. Both clauses are
// satelle's — there is no human at this prompt to ask.
func reworkCoderPolicy(binding, tools string, seat func() (seatInfo, bool, error), onDecision func(agentcli.PermissionRequest, bool, string)) agentcli.PermissionPolicy {
	grantAllows := agentcli.GrantAllowsMutators(tools)
	if onDecision == nil {
		onDecision = func(agentcli.PermissionRequest, bool, string) {}
	}
	return func(req agentcli.PermissionRequest) agentcli.PermissionDecision {
		if !agentcli.IsMutatorRequest(req) {
			onDecision(req, true, "policy")
			return agentcli.PermissionDecision{Allow: true}
		}
		if !grantAllows {
			onDecision(req, false, "grant")
			return agentcli.PermissionDecision{Allow: false}
		}
		if seat == nil {
			onDecision(req, false, "policy")
			return agentcli.PermissionDecision{Allow: false}
		}
		info, _, err := seat()
		if err != nil || !dispatchedPerformerPermitted(info, binding) {
			onDecision(req, false, "policy")
			return agentcli.PermissionDecision{Allow: false}
		}
		onDecision(req, true, "policy")
		return agentcli.PermissionDecision{Allow: true}
	}
}

// reworkConsultPolicy is the consultant side's ceiling: mutators are denied
// outright, whatever the seat says. Its binding grant is already read-only; the
// policy is the second, non-negotiable refusal, because a consultant that can
// edit is a reviewer marking its own work.
func reworkConsultPolicy(onDecision func(agentcli.PermissionRequest, bool, string)) agentcli.PermissionPolicy {
	if onDecision == nil {
		onDecision = func(agentcli.PermissionRequest, bool, string) {}
	}
	return func(req agentcli.PermissionRequest) agentcli.PermissionDecision {
		if agentcli.IsMutatorRequest(req) {
			onDecision(req, false, "policy")
			return agentcli.PermissionDecision{Allow: false}
		}
		onDecision(req, true, "policy")
		return agentcli.PermissionDecision{Allow: true}
	}
}
