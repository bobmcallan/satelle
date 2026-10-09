package verb_test

import (
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_29ce243b AC4: the same-role advisory refuses no transition — engage leaves
// the entry state with the executor and orchestrator seats both in-loop.
func TestEngageAllowsExecutorAndOrchestratorBothInLoop(t *testing.T) {
	withWiring(t)
	wireWithWorkflows(t, engageWF)
	verb.SetAgentsConfig(config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: config.DefaultReviewerCommand},
		Agents:   map[string]config.AgentBinding{"orchestrator": {Command: "in-loop"}},
	}, nil)

	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "Engage same role", "body": "goal", "acceptance_criteria": "1. done",
		"category": "feature",
	}), &created)

	var engaged workitem.Item
	json.Unmarshal(call(t, "story-set", map[string]any{"id": created.ID, "status": "plan"}), &engaged)
	if engaged.Status != "plan" {
		t.Fatalf("same-role seats must not refuse engage, status=%q", engaged.Status)
	}
}
