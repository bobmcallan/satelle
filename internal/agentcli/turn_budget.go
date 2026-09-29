package agentcli

import (
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
// transports do when the template carries {max_turns}; acp never does. The
// unavailable reason names the adapter.
func TurnBudgetSupport(iface, command string) Capability {
	fields := strings.Fields(command)
	if len(fields) == 0 || strings.EqualFold(fields[0], "in-loop") {
		return no("in-loop: no process is spawned, so there is no harness to hand a turn budget to")
	}
	if strings.EqualFold(strings.TrimSpace(iface), InterfaceACP) {
		return no(acpTurnBudgetReason)
	}
	if !strings.Contains(command, "{max_turns}") {
		return no(fmt.Sprintf("%s: the command template carries no {max_turns} placeholder, so the harness is not told the turn budget", AdapterLabel(iface, command)))
	}
	return yes()
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
// count, so a recorded absence is never read as zero turns.
func TurnsUnavailableReason(iface, command string) string {
	return fmt.Sprintf("%s: the run's output carried no turn count (num_turns)", AdapterLabel(iface, command))
}
