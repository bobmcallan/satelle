package agentcli

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// Turn budget (sty_a7914904). A repo may set turn_budget on a binding or step;
// satelle passes it to the harness through the {max_turns} placeholder where the
// adapter has a turn-limit flag, and otherwise records that it could not — never
// a silent pass-through and never a number of satelle's own
// (satelle-agent-agnostic §1, §2). The flag itself is authored in the binding's
// command template (`--max-turns {max_turns}`), so it lives in configuration.

// acpTurnBudgetReason is why a grok agent stdio (ACP) session cannot be told a
// turn budget: the spawn line carries no placeholders, and the ACP session
// protocol has no turn-limit option.
const acpTurnBudgetReason = "grok agent stdio has no turn-limit argv or session option, so a turn_budget is recorded and checked against reported turns only"

// AdapterLabel names the adapter behind a binding's transport and command as
// "<provider> <transport>" — the wording capability reasons and recorded
// unavailables use. A harness satelle has no adapter for is "unknown", never
// assumed to be Claude.
func AdapterLabel(iface, command string) string {
	harness := HarnessUnknown
	if fields := strings.Fields(command); len(fields) > 0 {
		harness = adapterOf(fields[0], fields[1:])
	}
	transport := strings.ToLower(strings.TrimSpace(iface))
	if transport == "" {
		transport = InterfaceCommand
	}
	return harness + " " + transport
}

// TurnBudgetSupport says whether a dispatch of a binding with this transport and
// command template hands the harness its turn budget. command and stream
// transports do when the template carries {max_turns}; acp never does; pi-rpc
// always does, because satelle aborts the run itself at the budget. The
// unavailable reason names the adapter.
func TurnBudgetSupport(iface, command string) Capability {
	fields := strings.Fields(command)
	if len(fields) == 0 || strings.EqualFold(fields[0], "in-loop") {
		return no("in-loop: no process is spawned, so there is no harness to hand a turn budget to")
	}
	switch strings.ToLower(strings.TrimSpace(iface)) {
	case InterfaceACP:
		return no(acpTurnBudgetReason)
	case InterfacePiRPC:
		return yes()
	}
	if !strings.Contains(command, "{max_turns}") {
		return no(fmt.Sprintf("%s: the command template carries no {max_turns} placeholder, so the harness is not told the turn budget", AdapterLabel(iface, command)))
	}
	return yes()
}

// EffectiveTurnBudget describes, for an operator-facing report, the turn budget a
// dispatch of a binding with this transport and command template runs with and
// where it comes from: "turn budget: N (turn_budget)" when the template's
// {max_turns} placeholder is filled from the configured turn_budget (or satelle
// itself enforces it, for pi-rpc),
// "turn budget: N (command --max-turns)" when the template itself carries the
// flag, and otherwise "turn budget: none (<adapter-named reason>)". configured is
// the resolved turn_budget (zero when unset). Every adapter decision lives here,
// so a caller reports every binding the same way.
func EffectiveTurnBudget(iface, command string, configured int) string {
	fields := strings.Fields(command)
	if strings.EqualFold(strings.TrimSpace(iface), "cloud") {
		return "turn budget: none (" + AdapterLabel(iface, command) + ": a cloud session is not spawned from the command template, so no turn limit is passed)"
	}
	selfEnforced := strings.EqualFold(strings.TrimSpace(iface), InterfacePiRPC)
	if (selfEnforced || strings.Contains(command, "{max_turns}")) && TurnBudgetSupport(iface, command).Available {
		if configured > 0 {
			return fmt.Sprintf("turn budget: %d (turn_budget)", configured)
		}
		return "turn budget: none (" + AdapterLabel(iface, command) + ": no turn_budget set)"
	}
	if !strings.EqualFold(strings.TrimSpace(iface), InterfaceACP) && len(fields) > 0 && !strings.EqualFold(fields[0], "in-loop") {
		if n, ok := literalMaxTurns(fields); ok {
			return "turn budget: " + n + " (command --max-turns)"
		}
	}
	return "turn budget: none (" + TurnBudgetSupport(iface, command).Reason + ")"
}

// literalMaxTurns returns the value of a literal `--max-turns N` (or
// `--max-turns=N`) argument in a command template's fields.
func literalMaxTurns(fields []string) (string, bool) {
	for i, f := range fields {
		if v, ok := strings.CutPrefix(f, "--max-turns="); ok && v != "" {
			return v, true
		}
		if f == "--max-turns" && i+1 < len(fields) && !strings.HasPrefix(fields[i+1], "-") {
			return fields[i+1], true
		}
	}
	return "", false
}

// applyTurns records the run's own turn count when the output carried one; an
// absent count leaves TurnsAvailable false, never a zero.
func applyTurns(u *UsageResult, n *int) {
	if n == nil {
		return
	}
	u.Turns, u.TurnsAvailable = *n, true
}

// TurnsUnavailableReason is the adapter-named reason a run reported no turn
// count, so a recorded absence is never read as zero turns. A transport that
// never carries one names that, instead of blaming the run's output.
func TurnsUnavailableReason(iface, command string) string {
	label := AdapterLabel(iface, command)
	switch strings.ToLower(strings.TrimSpace(iface)) {
	case "cloud":
		return "cloud: a cloud session is not spawned from a command template, so no turn count is read"
	case InterfaceACP:
		return label + ": the session protocol reports no turn count"
	case InterfacePiRPC:
		return label + ": the run ended before satelle counted its turns"
	}
	return fmt.Sprintf("%s: the run's output carried no turn count (num_turns)", label)
}

// ReportsToolEvents says whether a dispatch with this transport and command
// template emits a tool-start event per tool call, so satelle can count them.
// stream, acp and pi-rpc always do; a command transport does only when its output format
// is a JSONL event stream (stream-json, streaming-json) — its one-shot json
// envelope and plain output carry the answer, never the tool calls. A zero count
// from a transport that reports events is a measured zero; every other transport
// has no count at all (ToolCallsUnavailableReason).
func ReportsToolEvents(iface, command string) bool {
	switch strings.ToLower(strings.TrimSpace(iface)) {
	case InterfaceStream, InterfaceACP, InterfacePiRPC:
		return true
	case "cloud":
		return false
	}
	fields := strings.Fields(command)
	if len(fields) < 2 || strings.EqualFold(fields[0], "in-loop") {
		return false
	}
	for _, format := range flagValues(fields[1:], "--output-format") {
		if f := strings.ToLower(format); f == "stream-json" || f == "streaming-json" {
			return true
		}
	}
	return false
}

// ToolCallsUnavailableReason is the adapter-named reason a dispatch with this
// transport and command template has no tool-call count (ReportsToolEvents
// false), so a recorded absence is never read as zero calls.
func ToolCallsUnavailableReason(iface, command string) string {
	if strings.EqualFold(strings.TrimSpace(iface), "cloud") {
		return "cloud: a cloud session is not spawned from a command template, so no tool events are seen"
	}
	return AdapterLabel(iface, command) + ": the transport emits no tool events (its output format is not stream-json or streaming-json), so tool calls are not counted"
}

// ErrMaxTurns marks a run the harness ended because it reached its turn cap
// (the configured turn_budget / --max-turns). It is its own outcome: the run was
// cut short, it did not reach a verdict, and it is not a reject.
var ErrMaxTurns = errors.New("the harness ended the run at its turn cap (max turns reached)")

// maxTurnsMarkers are the provider spellings of a turn-cap stop, matched
// case-insensitively: grok prints "Max turns reached", and claude's result
// envelope carries subtype "error_max_turns". Nothing outside this file knows
// them.
var maxTurnsMarkers = [][]byte{[]byte("max turns reached"), []byte("error_max_turns")}

// DetectMaxTurns reports whether a run's output says the harness stopped it at
// its turn cap. Callers consult it only for a run that produced no verdict.
func DetectMaxTurns(out []byte) bool {
	lower := bytes.ToLower(out)
	for _, m := range maxTurnsMarkers {
		if bytes.Contains(lower, m) {
			return true
		}
	}
	return false
}
