package agentcli

import (
	"fmt"
	"strings"
	"time"
)

// Per-harness facts about how a DRIVING session waits on a command
// (sty_c4b92c9e, epic:token-accountability). A gate-running verb can outlast a
// harness's own background cutoff; once the harness backgrounds the command the
// driver re-enters the model to learn how it ended, and every re-entry is a
// full model call. These are facts about the harness, so they live here beside
// the other adapter facts — never in the verb or CLI layer
// (satelle-agent-agnostic §1) — and each is either a value or an adapter-named
// "unavailable"/"unverified" (§2), never a silent default.

// GateWaitMargin is what AgentWaitBound leaves between the wait it allows and
// the shortest background cutoff: process start, store open and the replay of
// a finished verdict all happen inside the harness's clock, not ours.
const GateWaitMargin = 5 * time.Second

// DefaultBackgroundCutoff is the cutoff assumed for a harness with no row —
// nothing unrecognised is assumed generous (§3). It is deliberately no longer
// than the shortest measured cutoff.
const DefaultBackgroundCutoff = 10 * time.Second

// HarnessFacts is one harness's row.
type HarnessFacts struct {
	// Harness is the token recorded in telemetry: claude or grok.
	Harness string
	// BackgroundCutoff is how long a foreground command may run before the
	// harness backgrounds it (or stops it) on the driver's behalf.
	BackgroundCutoff time.Duration
	// CutoffBasis says where the number comes from — a measured harness
	// behaviour, or the conservative floor used because nothing was measured.
	CutoffBasis string
	// CompletionNotification: the harness can deliver a finished gate's verdict
	// into the driving session without the driver asking. Polling is not this.
	CompletionNotification Capability
	// InTurnWake is how a finished gate reaches the driving session inside a
	// turn, what budget that wake has, and what happens when it is spent
	// (sty_3a2d3b7e, epic:gate-wake). A completion notification that exists is
	// not a wake that cannot run out: each row names its budget, its basis, or
	// an adapter-named "unavailable"/"unverified".
	InTurnWake string
	// PromptContext: the harness delivers the prompt hook's additionalContext to
	// the model — the catch-up channel for a verdict that missed its wake.
	PromptContext Capability
	// PromptContextNote qualifies PromptContext (how it is delivered), appended
	// to the yes/unavailable cell.
	PromptContextNote string
}

// HarnessFactsTable returns the table in the order help prints it.
func HarnessFactsTable() []HarnessFacts {
	return []HarnessFacts{
		{
			Harness:          HarnessClaude,
			BackgroundCutoff: 2 * time.Minute,
			CutoffBasis:      "the Bash tool's default timeout (120s)",
			// The satelle scaffold installs a Stop hook on claude, and a Stop hook
			// that answers {"decision":"block","reason":…} keeps the session going
			// with that reason as its next input — a wake with no driver call.
			CompletionNotification: yes(),
			// Measured on the installed claude 2.1.288:
			// CLAUDE_CODE_STOP_HOOK_BLOCK_CAP defaults to 8, and a live always-block
			// probe (claude -p haiku, --tools "", Stop hook always blocking, session
			// db658341-256f-413c-ac4e-c6c48922fbf5, 2026-10-03) was overridden after
			// 9 consecutive Stop blocks ("A hook blocked the turn from ending 9
			// consecutive times").
			InTurnWake:        "a Stop-block wakes the session; cap 8 (CLAUDE_CODE_STOP_HOOK_BLOCK_CAP default) — measured on claude 2.1.288: a live always-block Stop probe was overridden after 9 consecutive blocks (session db658341-256f-413c-ac4e-c6c48922fbf5, 2026-10-03). Past the cap, or after a StopFailure/SessionEnd, a gate is delivered by resuming the same session with the verdict as the prompt (sty_7e4393fc)",
			PromptContext:     yes(),
			PromptContextNote: "delivered, and the scaffold's engaged reminder rides it",
		},
		{
			Harness:          HarnessGrok,
			BackgroundCutoff: 15 * time.Second,
			CutoffBasis:      "grok backgrounds any command past 15s (sty_c4b92c9e)",
			// The satelle scaffold installs Stop and UserPromptSubmit hooks on grok.
			// Observed on a live headless grok 1.0.41 session (sty_c4b92c9e,
			// attachment grok-dogfood-run-1): the command returned a handle, the
			// driver ended its turn, and the Stop hook's block reason woke the
			// session with the verdict — three model calls in all, no polling.
			CompletionNotification: yes(),
			// Measurement: the grok binary's stop_gate.rs notes and its porting
			// list (grok 1.0.41 observed). A Stop-block and a non-error Stop
			// feedback each count as a continuation.
			InTurnWake:    "cap 8 continuations (a Stop-block and a non-error Stop feedback each count); after 8 the gate is overridden, hooks are not consulted and the turn ends; the counter resets on the next user prompt — measured from grok's stop_gate.rs notes and porting list. Past the cap a gate is delivered by resuming the same session with the verdict as the prompt (sty_eac9b28d)",
			PromptContext: no("grok: UserPromptSubmit additionalContext is discarded"),
		},
		{
			Harness:          HarnessPi,
			BackgroundCutoff: 10 * time.Minute,
			// Measured 2026-09-29 on a live pi 0.87.1 session in this repo: a
			// foreground bash call held the foreground for 25 minutes under an
			// explicit timeout, and 45s with no timeout at all. pi does not
			// background a long command the way grok does at 15s, so a
			// gate-running verb completes in the foreground and replayGate
			// delivers its own verdict — no driver re-entry, no handoff.
			CutoffBasis: "a live pi 0.87.1 foreground call held 25m with an explicit timeout and 45s with none; pi does not auto-background a long command",
			// The verdict arrives through the foreground replay, which calls the
			// same exclusive Claim the harness Stop hooks call — so delivery is
			// exactly-once by construction, not by a pi-specific hook. This
			// matters: without this row pi fell to the HarnessUnknown floor
			// (10s), every gate detached at 10s, and nothing ever claimed the
			// finished handle — twelve stranded gates on 2026-09-29, each with
			// a correct reviewer verdict that never landed.
			CompletionNotification: yes(),
			// Grounded in the satelle pi extension (satelle_pi_extension.ts.tmpl).
			InTurnWake:        "Stop cannot veto: the extension turns a block into a user message that starts another turn; a non-interactive run that exits on settle is not held; no budget recorded",
			PromptContext:     yes(),
			PromptContextNote: "the extension injects it into the system prompt via before_agent_start",
		},
	}
}

// FactsFor returns the row for harness. A harness with no row (including
// HarnessUnknown) gets the conservative floor and an "unavailable" notification
// that names it — never a claim of a wake nothing wired.
func FactsFor(harness string) HarnessFacts {
	h := strings.TrimSpace(harness)
	for _, f := range HarnessFactsTable() {
		if f.Harness == h {
			return f
		}
	}
	if h == "" {
		h = HarnessUnknown
	}
	return HarnessFacts{
		Harness:                h,
		BackgroundCutoff:       DefaultBackgroundCutoff,
		CutoffBasis:            "no measurement for this harness — the conservative floor",
		CompletionNotification: no(h + ": no completion-notification path is wired for this harness"),
		InTurnWake:             "unavailable: " + h + ": no in-turn wake path is recorded for this harness",
		PromptContext:          no(h + ": no prompt additionalContext path is recorded for this harness"),
	}
}

// PromptContextCell is the help-table spelling of PromptContext: the
// yes/unavailable cell, qualified by the note when one is recorded.
func (f HarnessFacts) PromptContextCell() string {
	if f.PromptContextNote == "" {
		return f.PromptContext.Cell()
	}
	return f.PromptContext.Cell() + " — " + f.PromptContextNote
}

// GateForegroundBudget is the LOW end of how long a gate-running verb holds the
// foreground, not a typical or worst case. Its basis is measured: the driver
// windows around gate-running commands in this repo's own ledger
// (`satelle story cost`, sty_c4b92c9e / sty_7f3e6fd3 / sty_8ee31f26) run from
// 53s to 6m55s — 53s, 1m2s, 1m16s, 1m42s, 2m41s, 4m44s, 6m55s — and a single
// reviewer alone takes 9s–50s. So a harness whose cutoff is under a minute
// cannot wait out even the quickest gate, and a hand-off is certainly needed.
// A harness above it (claude, 120s) is NOT guaranteed to finish: a multi-gate
// edge can exceed 120s, and that repo sets `[gate] handoff = "on"`. `auto` is
// therefore a floor for when a hand-off is unavoidable, not a promise that the
// foreground is safe.
const GateForegroundBudget = time.Minute

// HandoffNeeded reports whether any harness in play has a background cutoff
// below GateForegroundBudget. A harness with no row takes FactsFor's
// conservative floor, so it counts as needing a hand-off (§3): nothing
// unrecognised is assumed generous. No harness in play means the floor applies.
func HandoffNeeded(harnesses []string) bool {
	if len(harnesses) == 0 {
		return DefaultBackgroundCutoff < GateForegroundBudget
	}
	for _, h := range harnesses {
		if FactsFor(h).BackgroundCutoff < GateForegroundBudget {
			return true
		}
	}
	return false
}

// AgentWaitBound is the longest an agent-facing gate-running verb may stay in
// the foreground: the shortest background cutoff of any harness in play, less
// GateWaitMargin, so the command always returns before that harness can
// background it. No harness in play means the floor applies (§3).
func AgentWaitBound(harnesses []string) time.Duration {
	shortest := time.Duration(0)
	for _, h := range harnesses {
		c := FactsFor(h).BackgroundCutoff
		if shortest == 0 || c < shortest {
			shortest = c
		}
	}
	if shortest == 0 {
		shortest = DefaultBackgroundCutoff
	}
	bound := shortest - GateWaitMargin
	if bound < time.Second {
		bound = time.Second
	}
	return bound
}

// harnessFactsColumns are the harness-facts table headings, in cell order.
var harnessFactsColumns = []string{"background cutoff", "completion notification", "in-turn wake / budget", "prompt additionalContext"}

// HarnessFactsTableMarkdown renders HarnessFactsTable as the markdown table
// `satelle help agent-dispatch` carries, so the docs cannot say a harness can
// wake its driver when the code records that it cannot.
func HarnessFactsTableMarkdown() string {
	var b strings.Builder
	b.WriteString("| harness | " + strings.Join(harnessFactsColumns, " | ") + " |\n")
	b.WriteString("| --- |" + strings.Repeat(" --- |", len(harnessFactsColumns)) + "\n")
	for _, f := range HarnessFactsTable() {
		fmt.Fprintf(&b, "| %s | %s — %s | %s | %s | %s |\n", f.Harness, f.BackgroundCutoff, f.CutoffBasis, f.CompletionNotification.Cell(), f.InTurnWake, f.PromptContextCell())
	}
	return b.String()
}
