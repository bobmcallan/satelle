package agentstep

import (
	"context"
	"fmt"
	"sync"

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
// consult session — goes through it before any process starts. It never refuses
// (sty_2d5e583a): a binding no adapter path can keep inside its grant runs, with
// the gap shown once on stderr, recorded on the ledger and described on the
// agent_invocation row. It marks req read-only so the adapter trims what the
// harness offers and denies a tool outside the grant by name where it can, and
// hooks the runtime observations (a peer with no ask mode, a tool that ran
// outside the grant) into the same ledger and warning.
//
// attest is the binding's isolation = "operator-attested" acknowledgement: it
// downgrades the configured-gap warning to an info note (nothing on stderr; the
// ledger still records the limitation). The returned bool reports that the
// attestation — not an adapter — admitted an unrecognised harness, in which case
// no offered-tool count is ever recorded.
func (g *Engine) isolateReviewer(ctx context.Context, d reviewerDispatch, runner agentcli.Runner, attest bool, req *agentcli.Request) (verb.ToolIsolation, bool) {
	req.ReadOnly = true
	actor := d.Actor
	if actor == "" {
		actor = "reviewer"
	}
	gaps := agentcli.PreflightRunner(runner, req.AllowedTools)
	attestedUnknown := attest && agentcli.UnrecognisedRunner(runner)
	var iso verb.ToolIsolation
	if attestedUnknown {
		iso = toolIsolation(agentcli.AttestedIsolation(d.Section))
	} else {
		iso = toolIsolation(agentcli.DescribeReviewer(runner, *req))
	}
	if len(gaps) > 0 {
		summary := agentcli.GapSummary(gaps)
		if !attestedUnknown {
			limit := fmt.Sprintf("binding %q (%s): %s", d.Section, gaps[0].Adapter, summary)
			if iso.IsolationLimitation != "" {
				limit += " | " + iso.IsolationLimitation
			}
			iso.IsolationLimitation = limit
		}
		if iso.OfferedToolsSource == "" {
			iso.OfferedToolsSource = "unavailable: " + gaps[0].Adapter + " offered tools are not verified"
		}
		g.telemetryEvent(ctx, d.StoryID, actor, "reviewer-isolation-warned", map[string]any{
			"skill": d.Skill, "step": d.Step, "agent": d.Section, "adapter": gaps[0].Adapter,
			"gap": summary, "fix": agentcli.GapFix(gaps), "acknowledged": attest,
		})
		if !attest {
			g.warnf("satelle: warning: reviewer binding %q (%s): %s — fix: %s", d.Section, gaps[0].Adapter, summary, agentcli.GapFix(gaps))
		}
	}

	var mu sync.Mutex
	seen := map[string]bool{}
	req.OnIsolation = func(n agentcli.IsolationNote) {
		mu.Lock()
		dup := seen[n.Detail]
		seen[n.Detail] = true
		mu.Unlock()
		if dup {
			return
		}
		kind := "reviewer-isolation-gap"
		if n.Breach {
			kind = "reviewer-isolation-breach"
		}
		g.telemetryEvent(ctx, d.StoryID, actor, kind, map[string]any{
			"skill": d.Skill, "step": d.Step, "agent": d.Section, "adapter": n.Adapter, "detail": n.Detail,
		})
		// An observed breach is behaviour, not a configured gap, so attestation
		// never silences it. A runtime gap only warns when the binding had no
		// configured gap already warning of the same limitation.
		if n.Breach || (len(gaps) == 0 && !attest) {
			g.warnf("satelle: warning: reviewer binding %q (%s): %s", d.Section, n.Adapter, n.Detail)
		}
	}
	return iso, attestedUnknown
}

// warnf writes one warning line to the engine's warning writer (stderr by default).
func (g *Engine) warnf(format string, a ...any) {
	if g.warnOut != nil {
		fmt.Fprintf(g.warnOut, format+"\n", a...)
	}
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
