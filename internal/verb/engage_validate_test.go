package verb_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const engageWF = `---
name: engage-wf
type: workflow
applies_to: ["*"]
---

` + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  plan        [agent=executor]
  cancelled   [shape=Msquare, agent=reviewer, prompt="@skill:cancel"]
  done        [shape=Msquare]
  backlog -> plan
  backlog -> cancelled
  plan -> done
}
` + "```" + `
`

// TestEngageRefusesBrokenAgents proves the engage precondition (sty_93eec36d):
// leaving the workflow entry state for a non-cancel target refuses when
// agents.toml is broken; cancel from entry is still allowed.
func TestEngageRefusesBrokenAgents(t *testing.T) {
	wireWithWorkflows(t, map[string]string{"engage-wf": engageWF})
	verb.SetAgentsConfig(config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: "not-a-real-cli"},
	}, nil)
	t.Cleanup(verb.ClearAgentsConfig)

	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "Engage me", "body": "goal", "acceptance_criteria": "1. done",
		"category": "feature",
	}), &created)

	err := dispatchErr(t, "story-set", map[string]any{"id": created.ID, "status": "plan"})
	msg := err.Error()
	if !strings.Contains(msg, "validation failed") && !strings.Contains(msg, "not-a-real") && !strings.Contains(msg, "agents.toml") {
		t.Fatalf("expected engage validate refusal, got: %v", err)
	}

	// Cancel from entry must NOT be blocked by the engage validate.
	var cancelled workitem.Item
	json.Unmarshal(call(t, "story-set", map[string]any{"id": created.ID, "status": "cancelled"}), &cancelled)
	if cancelled.Status != "cancelled" {
		t.Fatalf("cancel from entry should enact under broken agents, status=%q", cancelled.Status)
	}
}

// TestEngageAllowsHealthyAgents: green agents.toml lets engage proceed.
func TestEngageAllowsHealthyAgents(t *testing.T) {
	wireWithWorkflows(t, map[string]string{"engage-wf": engageWF})
	verb.SetAgentsConfig(config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: config.DefaultReviewerCommand},
	}, nil)
	t.Cleanup(verb.ClearAgentsConfig)

	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "Engage healthy", "body": "goal", "acceptance_criteria": "1. done",
		"category": "feature",
	}), &created)

	var engaged workitem.Item
	json.Unmarshal(call(t, "story-set", map[string]any{"id": created.ID, "status": "plan"}), &engaged)
	if engaged.Status != "plan" {
		t.Fatalf("healthy agents must allow engage, status=%q", engaged.Status)
	}
}
