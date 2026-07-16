package verb

import (
	"context"
	"encoding/json"

	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/substrate"
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
		filtered := workflows[:0]
		for _, w := range workflows {
			if w.Name == req.Workflow {
				filtered = append(filtered, w)
			}
		}
		workflows = filtered
	}

	if dataDir == "" {
		view.AgentsError = "data dir not wired"
		return json.Marshal(view)
	}
	agents, err := config.LoadAgents(dataDir)
	if err != nil {
		view.AgentsError = err.Error()
		return json.Marshal(view)
	}
	// vars are optional for model resolution; empty map is fine.
	rep := agentvalidate.Validate(agents, nil, workflows)
	view.Allocations = rep.Gates
	return json.Marshal(view)
}
