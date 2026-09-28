package agentstep

import (
	"context"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/verb"
)

// reviewerDispatch names one role=reviewer dispatch for the isolation ledger row.
type reviewerDispatch struct {
	StoryID string
	Actor   string // telemetry actor; empty is "reviewer"
	Skill   string
	Step    string
	Section string // the binding's agents.toml section
}

// isolateReviewer is the ONE reviewer tool-isolation path (sty_ef3efb51): every
// role=reviewer dispatch — a gate verdict, the step summariser and a live
// consult session — goes through it before any process starts. It refuses a
// binding no adapter path can keep inside its grant (ledgering the refusal),
// marks req read-only so the adapter trims what the harness offers and denies a
// tool outside the grant by name, and describes what the spawn offers for the
// agent_invocation row. attested is the binding's operator-attested declaration;
// the returned bool reports that it — not an adapter — admitted the harness,
// in which case no count is ever recorded.
func (g *Engine) isolateReviewer(ctx context.Context, d reviewerDispatch, runner agentcli.Runner, attest bool, req *agentcli.Request) (verb.ToolIsolation, bool, error) {
	if perr := agentcli.PreflightRunner(runner, req.AllowedTools, attest); perr != nil {
		refuser := d.Actor
		if refuser == "" {
			refuser = "reviewer"
		}
		g.telemetryEvent(ctx, d.StoryID, refuser, "reviewer-isolation-refused", map[string]any{
			"skill": d.Skill, "step": d.Step, "agent": d.Section, "reason": perr.Error(),
		})
		return verb.ToolIsolation{}, false, perr
	}
	req.ReadOnly = true
	if attest && agentcli.UnrecognisedRunner(runner) {
		return toolIsolation(agentcli.AttestedIsolation(d.Section)), true, nil
	}
	return toolIsolation(agentcli.DescribeReviewer(runner, *req)), false, nil
}

// isolationFields stamps a ToolIsolation onto an agent_invocation payload map.
func isolationFields(m map[string]any, iso verb.ToolIsolation) {
	if iso.OfferedToolCount != nil {
		m["offered_tool_count"] = *iso.OfferedToolCount
	}
	if iso.OfferedToolsSource != "" {
		m["offered_tools_source"] = iso.OfferedToolsSource
	}
	if iso.IsolationLimitation != "" {
		m["isolation_limitation"] = iso.IsolationLimitation
	}
}
