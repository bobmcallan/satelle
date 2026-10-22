package agentstep

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// SetOneShotAgents wires the resolver PerformExecutorStep uses: name's binding
// through AgentsConfig.EffectiveBinding(UseOneShot) over the raw binding, with
// the section's own baseline default applied first (an unset [executor] command
// is the in-loop session, not a reviewer default). Nil refuses every
// PerformExecutorStep.
func (g *Engine) SetOneShotAgents(fn func(name string) (config.AgentBinding, bool)) {
	g.oneShotAgents = fn
}

// ExecutorBinding is the [executor] binding as a one-shot run would use it, and
// false when no resolver is wired. Its command is "in-loop" when the driving
// session itself performs executor-allocated steps — the case a headless caller
// must refuse rather than perform for it.
func (g *Engine) ExecutorBinding() (config.AgentBinding, bool) {
	if g.oneShotAgents == nil {
		return config.AgentBinding{}, false
	}
	return g.oneShotAgents("executor")
}

// PerformExecutorStep runs the [executor] binding once, headless, to perform the
// work of a step the workflow allocates to agent=executor — the work the driving
// session does by hand when the binding is in-loop. step is the status the story
// is in; its composed executor skills are the rubric, and notes (the reviewers'
// findings from a rejected edge, or "") are appended to it.
//
// It is the executor-allocated counterpart of DispatchExecutor, which performs a
// NAMED agent's step on entry: the same payload, invoke path, transport, budget
// and result record, resolved for the [executor] section. An in-loop binding is
// an error, never a quiet no-op; a performer that says reject comes back as a
// *verb.PerformerReject. It changes no status.
func (g *Engine) PerformExecutorStep(ctx context.Context, item workitem.Item, step, notes string) (verb.DispatchResult, error) {
	spec, wfName, err := g.activeSpec(ctx, item)
	if err != nil {
		return verb.DispatchResult{}, err
	}
	target, ok := spec.StateNamed(step)
	if !ok || target.Agent != "executor" {
		return verb.DispatchResult{}, fmt.Errorf("step %q is not allocated to agent=executor in workflow %q", step, wfName)
	}
	binding, found := g.ExecutorBinding()
	if !found {
		return verb.DispatchResult{}, fmt.Errorf("no agents layer is wired — cannot run the [executor] binding for step %q", step)
	}
	if config.IsInLoopCommand(binding.CommandTemplate()) {
		return verb.DispatchResult{}, fmt.Errorf(
			"step %q is allocated to agent=executor and the [executor] command in .satelle/workflows/agents.toml is in-loop: bind a headless CLI to perform it unattended", step)
	}
	runner, err := g.newRunner(binding.ResolvedInterface(), binding.CommandTemplate())
	if err != nil {
		var enf *ExecutableNotFoundError
		if errors.As(err, &enf) {
			return verb.DispatchResult{}, fmt.Errorf(
				"step %q is performed by the [executor] binding but its command %q is not executable on this machine (%v) — install it or rebind the executor",
				step, enf.Token, enf.Err)
		}
		return verb.DispatchResult{}, fmt.Errorf("[executor]: broken command in .satelle/workflows/agents.toml: %w", err)
	}
	if runner == nil {
		return verb.DispatchResult{}, fmt.Errorf("[executor] command resolves to no runner for step %q", step)
	}
	if err := g.wiringGuard(ctx, "executor", binding.CommandTemplate(), item.ID); err != nil {
		return verb.DispatchResult{}, err
	}
	if config.NeedsContextChannel(binding) && !config.BindingGrantsContextChannel(binding) {
		return verb.DispatchResult{}, fmt.Errorf(
			"the [executor] binding cannot perform step %q: its .satelle/workflows/agents.toml [executor] tools grant has no context channel (add `Bash(satelle:*)` for the satelle CLI, or %s under ~/.satelle/<repo-key>/stories/<id>/)",
			step, agentcli.ContextChannelHint(agentcli.AdapterName(binding.CommandTemplate())))
	}
	composed := spec.ExecutorSkillsFor(step, item.Tags)
	skill := firstStr(composed)
	if skill == "" {
		skill = target.Skill
	}
	rubric, err := g.composeSkillBodies(ctx, composed)
	if err != nil {
		return verb.DispatchResult{}, err
	}
	if n := strings.TrimSpace(notes); n != "" {
		rubric = strings.TrimSpace(rubric + "\n\n## Findings from the last presentation of this step's exit\n\n" +
			"The reviewers rejected the work this step produced. Address these, then stop:\n\n" + n)
	}
	modelResolved, modelSource := g.selectModel(ctx, binding, item.ID, target.Model, config.ModelSourceStep)
	mc := ModelChoice{Model: modelResolved, Source: modelSource}
	budget := config.ResolveBudget(
		config.Budget{Context: target.ContextBudget, Turns: target.TurnBudget},
		binding.Budget(), g.defaultBudget)
	binding.ContextBudget, binding.TurnBudget = budget.Context, budget.Turns
	timeout, err := binding.TimeoutDuration(g.agentTimeout)
	if err != nil {
		return verb.DispatchResult{}, fmt.Errorf("[executor]: invalid timeout in .satelle/workflows/agents.toml: %w", err)
	}
	idle, err := g.idleTimeoutFor("executor", binding)
	if err != nil {
		return verb.DispatchResult{}, fmt.Errorf("[executor]: invalid idle_timeout in .satelle/workflows/agents.toml: %w", err)
	}
	eventSink, sinkPath, closeSink := g.dispatchSink("executor", item.ID)
	if closeSink != nil {
		defer closeSink()
	}
	var sinkMu sync.Mutex
	onEvent := func(ev agentcli.Event) {
		if eventSink == nil {
			return
		}
		sinkMu.Lock()
		_, _ = io.WriteString(eventSink, agentcli.FormatEvent(ev))
		sinkMu.Unlock()
	}
	g.emitActivity(item.ID, "perform:"+step, 1, 1)
	if sinkPath != "" {
		g.emitProgress("performing step %s with the executor binding (may take several minutes)… live output: %s", step, sinkPath)
	} else {
		g.emitProgress("performing step %s with the executor binding (may take several minutes)…", step)
	}
	invokeBinding := binding
	invokeBinding.Model = mc.Model
	execPayload, consumed := g.performerPayload(ctx, item, step, skill, "executor", binding, target.ApplyCriteriaDoc)
	if consumed != nil {
		g.recordDocConsumed(ctx, item.ID, "executor", step, *consumed, 1)
	}
	invRes := g.Invoke(ctx, InvokeRequest{
		Binding:     invokeBinding,
		Section:     "executor",
		Rubric:      rubric,
		Payload:     execPayload,
		Charter:     executorCharter("executor", step, wfName),
		Expect:      ExpectPerform,
		Timeout:     timeout,
		IdleTimeout: idle,
		Runner:      runner,
		OnEvent:     onEvent,
		StoryID:     item.ID,
		Step:        step,
		Skill:       skill,
		Actor:       "executor",
	})
	res := g.performanceResult("executor", skill, binding, budget, mc, invRes)
	g.logExecutorRun("executor", item.ID, step, invRes.Stdout, invRes.Err)
	if invRes.Err != nil {
		g.telemetryEvent(ctx, item.ID, "executor", "agent-failure", withCause(map[string]any{
			"agent": "executor", "step": step, "outcome": classifyOutcome(invRes.Err),
			"duration_ms": res.DurationMs, "usage_available": res.UsageAvailable,
		}, invRes.Err))
		return res, fmt.Errorf("the [executor] binding failed performing step %q: %w", step, invRes.Err)
	}
	if aerr := RecordFindingAnswers(&res, execPayload.Findings, invRes.Stdout); aerr != nil {
		return res, fmt.Errorf("the [executor] binding reported answers for step %q that cannot be read: %w", step, aerr)
	}
	if dec, derr := parseDecision(invRes.Stdout); derr == nil && !dec.Accept {
		return res, &verb.PerformerReject{Notes: dec.Notes}
	}
	return res, nil
}
