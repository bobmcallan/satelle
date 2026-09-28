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
	// Harness is the token recorded in telemetry: claude, grok or codex.
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
		},
		{
			Harness:          HarnessCodex,
			BackgroundCutoff: DefaultBackgroundCutoff,
			CutoffBasis:      "not measured — the conservative floor",
			// codex's satelle scaffold installs no Stop hook (harnessHooks("codex")),
			// so nothing fires when a codex turn ends.
			CompletionNotification: no("codex: the satelle scaffold installs no Stop hook, so nothing fires when the turn ends; delivery lands only at the next UserPromptSubmit"),
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
	}
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
var harnessFactsColumns = []string{"background cutoff", "completion notification"}

// HarnessFactsTableMarkdown renders HarnessFactsTable as the markdown table
// `satelle help agent-dispatch` carries, so the docs cannot say a harness can
// wake its driver when the code records that it cannot.
func HarnessFactsTableMarkdown() string {
	var b strings.Builder
	b.WriteString("| harness | " + strings.Join(harnessFactsColumns, " | ") + " |\n")
	b.WriteString("| --- |" + strings.Repeat(" --- |", len(harnessFactsColumns)) + "\n")
	for _, f := range HarnessFactsTable() {
		fmt.Fprintf(&b, "| %s | %s — %s | %s |\n", f.Harness, f.BackgroundCutoff, f.CutoffBasis, f.CompletionNotification.Cell())
	}
	return b.String()
}
