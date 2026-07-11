package verb

import (
	"context"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// agentsLayer / agentsVars power the engage precondition (sty_93eec36d): when a
// story leaves its workflow entry state for a non-cancel target, agentvalidate
// runs so a broken agents.toml/workflow is refused before work begins. Unset
// (tests / pre-init) makes the precondition a no-op — same graceful degradation
// as the other package globals.
var (
	agentsWired bool
	agentsLayer config.AgentsConfig
	agentsVars  map[string]string
)

// SetAgentsConfig wires the agents layer + [vars] KV for the engage
// precondition. Call with the result of requireAgents at CLI bootstrap.
// Pass zero values / omit the call in tests that do not need the check.
func SetAgentsConfig(agents config.AgentsConfig, vars map[string]string) {
	agentsLayer = agents
	agentsVars = vars
	agentsWired = true
}

// ClearAgentsConfig resets the engage precondition wiring (tests).
func ClearAgentsConfig() {
	agentsLayer = config.AgentsConfig{}
	agentsVars = nil
	agentsWired = false
}

// refuseBrokenAgentsOnEngage runs agentvalidate when a STORY is leaving its
// workflow entry state for a non-cancel target. Cancel/terminal park exits stay
// open so a broken repo can still be cancelled. Returns a refusal error naming
// the story and the joined problems; nil when the check is skipped or green.
func refuseBrokenAgentsOnEngage(ctx context.Context, current workitem.Item, toStatus string) error {
	if !agentsWired || current.Kind != workitem.KindStory {
		return nil
	}
	entry, ok := storyEntryState(ctx, current)
	if !ok || current.Status != entry {
		return nil // not leaving entry (or entry unresolvable — structure gate owns that)
	}
	if isCancelOrTerminalExit(ctx, current, toStatus) {
		return nil
	}
	idx, err := requireDocIndex()
	if err != nil {
		return nil // no index → no-op (pre-init / tests)
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return fmt.Errorf("satelle: engage validate: list workflows: %w", err)
	}
	report := agentvalidate.Validate(agentsLayer, agentsVars, wfs)
	if report.OK() {
		return nil
	}
	return fmt.Errorf(
		"satelle: refusing to engage story %s — agents.toml/workflow validation failed:\n  - %s\nFix with `satelle agent validate`, then retry",
		current.ID, strings.Join(report.Problems, "\n  - "))
}

// isCancelOrTerminalExit reports whether toStatus is a cancel exit (or a bare
// terminal park) from the story's governing workflow — those must stay open even
// when agents.toml is broken so the operator can abandon the story.
func isCancelOrTerminalExit(ctx context.Context, item workitem.Item, to string) bool {
	if strings.EqualFold(to, "cancelled") || strings.EqualFold(to, "canceled") {
		return true
	}
	idx, err := requireDocIndex()
	if err != nil {
		return false
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return false
	}
	wf, ok := wfgovern.GoverningWorkflow(wfs, item)
	if !ok {
		return false
	}
	spec, ok := wfdot.Parse(wf.Body)
	if !ok {
		return false
	}
	for _, st := range spec.States {
		if st.Name != to {
			continue
		}
		// Terminal reviewer-role states (cancelled, done) — only cancelled is
		// intended as an "abandon" path from entry; done from entry is rare but
		// treating any terminal target as exempt keeps a broken repo unstuck.
		if st.Terminal {
			return true
		}
		// Agent=reviewer + no outgoing edges is the authored cancel shape even
		// without Terminal set on the parse path.
		if st.Agent == "reviewer" && !hasOutgoing(spec, st.Name) {
			return true
		}
	}
	return false
}

func hasOutgoing(spec wfdot.Spec, name string) bool {
	for _, tr := range spec.Transitions {
		if tr.From == name {
			return true
		}
	}
	return false
}
