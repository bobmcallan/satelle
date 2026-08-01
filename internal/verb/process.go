package verb

import (
	"context"
	"encoding/json"

	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/substrate"
	"github.com/bobmcallan/satelle/internal/wfgovern"
)

func init() {
	Register(&Verb{
		Name:        "process-view",
		Description: "Effective process: substrate provenance + workflow node bindings",
		Invoke:      processView,
	})
}

// processViewReq optionally scopes gate allocations to one workflow name.
type processViewReq struct {
	Workflow string `json:"workflow,omitempty"`
}

// ProcessView is the effective-process payload for CLI/web (sty_ba0eb5c6).
type ProcessView struct {
	Items       []substrate.Row                `json:"items"`
	Allocations []agentvalidate.GateAllocation `json:"allocations"`
	AgentsError string                         `json:"agents_error,omitempty"`
}

func processView(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireDocIndex()
	if err != nil {
		return nil, err
	}
	var req processViewReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	items, err := substrate.List(ctx, store)
	if err != nil {
		return nil, err
	}
	view := ProcessView{Items: items, Allocations: []agentvalidate.GateAllocation{}}

	workflows, err := store.List(ctx, "workflows")
	if err != nil {
		return nil, err
	}
	if req.Workflow != "" {
		// A derived route is TWO docs that only mean anything together, so naming
		// the route (or either half) keeps the pair (sty_d953c5d8).
		wantRoute := req.Workflow == wfgovern.DerivedRouteName || wfgovern.IsRouteSource(req.Workflow)
		filtered := workflows[:0]
		for _, w := range workflows {
			if w.Name == req.Workflow || (wantRoute && wfgovern.IsRouteSource(w.Name)) {
				filtered = append(filtered, w)
			}
		}
		workflows = filtered
	}

	if dataDir == "" {
		view.AgentsError = "data dir not wired"
		return json.Marshal(view)
	}
	// EFFECTIVE agents: the repo layer folded with the machine-wide profile
	// catalog (sty_c7dfeedf), so the allocation view shows the binding that will
	// actually run each gate rather than the un-resolved repo file.
	eff, err := config.LoadEffectiveAgents(dataDir, nil)
	if err != nil {
		view.AgentsError = err.Error()
		return json.Marshal(view)
	}
	rep := agentvalidate.Validate(eff.Agents, eff.Vars, workflows)
	view.Allocations = rep.Gates
	return json.Marshal(view)
}
