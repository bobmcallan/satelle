package agentstep

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const attemptedDispatchSkill = `---
name: architecture-alignment
type: skill
description: contracted test performer with bounded repair
output_name: design
output_type: design-note
output_required: true
output_schema: body
output_ac_coverage: true
attempt_repair_max: 1
attempt_escalate_max: 1
attempt_max_total: 3
attempt_initial_effort: low
attempt_repair_effort: medium
attempt_escalate_effort: high
attempt_escalate_binding: stronger
attempt_on_exhaust: fail
---
Return a structured artifact.`

type attemptRun struct {
	out string
	err error
}

type attemptRunner struct {
	runs     []attemptRun
	requests []agentcli.Request
}

func (r *attemptRunner) Name() string    { return "attempt-runner" }
func (r *attemptRunner) Command() string { return "attempt-runner" }
func (r *attemptRunner) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	r.requests = append(r.requests, req)
	i := len(r.requests) - 1
	if i >= len(r.runs) {
		return nil, errors.New("unexpected extra invocation")
	}
	return []byte(r.runs[i].out), r.runs[i].err
}

type attemptEvent struct {
	kind string
	data map[string]any
}

func attemptedEngine(t *testing.T, skill string, primary, stronger *attemptRunner) (*Engine, *[]attemptEvent) {
	t.Helper()
	docs := fakeDocs{workflow: dispatchWF, skillBody: skill, skillFound: true}
	g, _ := newEngine(t, "", docs)
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		switch name {
		case "architect":
			return config.AgentBinding{
				Command: "primary", Tools: "read_file", Model: "economy", Effort: "low",
			}, true
		case "stronger":
			return config.AgentBinding{
				Command: "stronger", Tools: "read_file", Model: "strong", Effort: "high",
			}, true
		default:
			return config.AgentBinding{}, false
		}
	})
	g.newRunner = func(_iface, command string) (agentcli.Runner, error) {
		switch command {
		case "primary":
			return primary, nil
		case "stronger":
			return stronger, nil
		default:
			return nil, errors.New("unknown runner")
		}
	}
	var events []attemptEvent
	g.SetTelemetry(func(_ context.Context, _, _, kind string, data map[string]any) {
		events = append(events, attemptEvent{kind: kind, data: data})
	})
	return g, &events
}

func attemptedItem() workitem.Item {
	return workitem.Item{
		ID: "sty_attempt", Status: "backlog",
		AcceptanceCriteria: "1. first\n2. second",
	}
}

func validAttempt(body string) string {
	raw, _ := json.Marshal(map[string]any{
		"artifact": map[string]any{"body": body},
	})
	return string(raw)
}

func TestArtifactAttemptsFirstPassStopsAfterOneCall(t *testing.T) {
	primary := &attemptRunner{runs: []attemptRun{{out: validAttempt("## AC1\nok\n## AC2\nok")}}}
	stronger := &attemptRunner{}
	g, events := attemptedEngine(t, attemptedDispatchSkill, primary, stronger)
	attached := 0
	g.SetArtifactAttacher(func(_ context.Context, _ workitem.Item, name, typ, _ string) (string, string, error) {
		attached++
		return name, typ, nil
	})
	res, err := g.DispatchExecutor(context.Background(), attemptedItem(), "plan")
	if err != nil {
		t.Fatal(err)
	}
	if len(primary.requests) != 1 || len(stronger.requests) != 0 || attached != 1 {
		t.Fatalf("primary=%d stronger=%d attached=%d", len(primary.requests), len(stronger.requests), attached)
	}
	if res.ArtifactName != "design" || len(*events) != 1 || (*events)[0].data["validator_ok"] != true {
		t.Fatalf("result=%#v events=%#v", res, *events)
	}
}

func TestArtifactAttemptsRepairReceivesDraftAndAllFindings(t *testing.T) {
	primary := &attemptRunner{runs: []attemptRun{
		{out: validAttempt("draft")},
		{out: validAttempt("## AC1\nfixed\n## AC2\nfixed")},
	}}
	g, events := attemptedEngine(t, attemptedDispatchSkill, primary, &attemptRunner{})
	var body string
	g.SetArtifactAttacher(func(_ context.Context, _ workitem.Item, name, typ, got string) (string, string, error) {
		body = got
		return name, typ, nil
	})
	if _, err := g.DispatchExecutor(context.Background(), attemptedItem(), "plan"); err != nil {
		t.Fatal(err)
	}
	if len(primary.requests) != 2 || !strings.Contains(primary.requests[1].SystemPrompt, `"draft":"draft"`) {
		t.Fatalf("repair request = %#v", primary.requests)
	}
	for _, want := range []string{"criterion 1", "criterion 2"} {
		if !strings.Contains(primary.requests[1].SystemPrompt, want) {
			t.Fatalf("repair prompt missing %q: %s", want, primary.requests[1].SystemPrompt)
		}
	}
	if !strings.Contains(body, "AC2") || len(*events) != 2 || (*events)[1].data["phase"] != "repair" {
		t.Fatalf("body=%q events=%#v", body, *events)
	}
}

func TestArtifactAttemptsEscalatesOnlyAfterRepairFails(t *testing.T) {
	primary := &attemptRunner{runs: []attemptRun{
		{out: validAttempt("initial invalid")},
		{out: validAttempt("repair invalid")},
	}}
	stronger := &attemptRunner{runs: []attemptRun{{out: validAttempt("## AC1\nstrong\n## AC2\nstrong")}}}
	g, events := attemptedEngine(t, attemptedDispatchSkill, primary, stronger)
	attached := 0
	g.SetArtifactAttacher(func(_ context.Context, _ workitem.Item, name, typ, _ string) (string, string, error) {
		attached++
		return name, typ, nil
	})
	if _, err := g.DispatchExecutor(context.Background(), attemptedItem(), "plan"); err != nil {
		t.Fatal(err)
	}
	if len(primary.requests) != 2 || len(stronger.requests) != 1 || attached != 1 {
		t.Fatalf("primary=%d stronger=%d attached=%d", len(primary.requests), len(stronger.requests), attached)
	}
	last := (*events)[2].data
	if last["phase"] != "escalate" || last["binding"] != "stronger" ||
		last["model"] != "strong" || last["effort"] != "high" ||
		last["escalation_reason"] != "validation-after-repair" {
		t.Fatalf("escalation event = %#v", last)
	}
}

func TestArtifactAttemptsExhaustedNeverAttaches(t *testing.T) {
	skill := strings.Replace(attemptedDispatchSkill, "attempt_max_total: 3", "attempt_max_total: 2", 1)
	primary := &attemptRunner{runs: []attemptRun{
		{out: validAttempt("invalid one")},
		{out: validAttempt("invalid two")},
	}}
	g, _ := attemptedEngine(t, skill, primary, &attemptRunner{})
	attached := 0
	g.SetArtifactAttacher(func(context.Context, workitem.Item, string, string, string) (string, string, error) {
		attached++
		return "", "", nil
	})
	_, err := g.DispatchExecutor(context.Background(), attemptedItem(), "plan")
	if err == nil || !strings.Contains(err.Error(), "bounded attempt") {
		t.Fatalf("err = %v", err)
	}
	if len(primary.requests) != 2 || attached != 0 {
		t.Fatalf("calls=%d attached=%d", len(primary.requests), attached)
	}
}

func TestArtifactAttemptsUnavailableUsageIsExplicit(t *testing.T) {
	primary := &attemptRunner{runs: []attemptRun{{out: validAttempt("## AC1\nok\n## AC2\nok")}}}
	g, events := attemptedEngine(t, attemptedDispatchSkill, primary, &attemptRunner{})
	g.SetArtifactAttacher(func(_ context.Context, _ workitem.Item, name, typ, _ string) (string, string, error) {
		return name, typ, nil
	})
	if _, err := g.DispatchExecutor(context.Background(), attemptedItem(), "plan"); err != nil {
		t.Fatal(err)
	}
	data := (*events)[0].data
	if data["usage_available"] != false {
		t.Fatalf("event = %#v", data)
	}
	if _, ok := data["tokens_total"]; ok {
		t.Fatalf("unavailable usage must not claim a numeric total: %#v", data)
	}
}

func TestArtifactAttemptsCancellationStopsImmediately(t *testing.T) {
	primary := &attemptRunner{runs: []attemptRun{{err: context.Canceled}}}
	g, _ := attemptedEngine(t, attemptedDispatchSkill, primary, &attemptRunner{})
	attached := 0
	g.SetArtifactAttacher(func(context.Context, workitem.Item, string, string, string) (string, string, error) {
		attached++
		return "", "", nil
	})
	_, err := g.DispatchExecutor(context.Background(), attemptedItem(), "plan")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if len(primary.requests) != 1 || attached != 0 {
		t.Fatalf("calls=%d attached=%d", len(primary.requests), attached)
	}
}

func TestArtifactAttemptsTokenBudgetStopsBeforeRepair(t *testing.T) {
	skill := strings.Replace(attemptedDispatchSkill,
		"attempt_on_exhaust: fail", "attempt_token_budget: 50\nattempt_on_exhaust: fail", 1)
	inner := validAttempt("invalid")
	envelope, _ := json.Marshal(map[string]any{
		"result": inner,
		"usage":  map[string]any{"input_tokens": 60, "output_tokens": 10},
	})
	primary := &attemptRunner{runs: []attemptRun{{out: string(envelope)}}}
	g, events := attemptedEngine(t, skill, primary, &attemptRunner{})
	_, err := g.DispatchExecutor(context.Background(), attemptedItem(), "plan")
	if err == nil || !strings.Contains(err.Error(), "token budget exhausted") {
		t.Fatalf("err = %v", err)
	}
	if len(primary.requests) != 1 || (*events)[0].data["usage_available"] != true ||
		(*events)[0].data["tokens_total"] != 70 {
		t.Fatalf("calls=%d events=%#v", len(primary.requests), *events)
	}
}
