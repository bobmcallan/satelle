package agentstep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// runArtifactAttempts executes one substrate-declared, bounded
// validate/repair/escalate policy. It retains invalid drafts only in memory and
// returns only a final validated artifact to the caller.
func (g *Engine) runArtifactAttempts(
	ctx context.Context,
	item workitem.Item,
	toStatus, skill, initialSection, rubric string,
	payload transitionPayload,
	charter string,
	initialBinding config.AgentBinding,
	initialRunner agentcli.Runner,
	timeout time.Duration,
	sink io.Writer,
	onEvent agentcli.EventHandler,
	contract agentartifact.Contract,
	policy agentartifact.AttemptPolicy,
) (InvokeResult, *agentartifact.Artifact, error) {
	maxTotal := policy.MaxTotal
	if maxTotal == 0 {
		maxTotal = 1 + policy.RepairMax + policy.EscalateMax
	}
	if maxTotal < 1 {
		maxTotal = 1
	}

	started := time.Now()
	section, binding, runner, runTimeout := initialSection, initialBinding, initialRunner, timeout
	phase := "initial"
	reason := ""
	if policy.InitialEffort != "" {
		binding.Effort = policy.InitialEffort
	}
	var (
		repairUsed   int
		escalateUsed int
		totalUsage   agentcli.UsageResult
		lastFindings []string
		lastDraft    string
		lastResult   InvokeResult
		attemptsRun  int
	)

	for attempt := 1; attempt <= maxTotal; attempt++ {
		attemptsRun = attempt
		attemptTimeout := runTimeout
		if policy.TimeBudget > 0 {
			remaining := policy.TimeBudget - time.Since(started)
			if remaining <= 0 {
				return withUsage(lastResult, totalUsage), nil, fmt.Errorf(
					"structured output attempt time budget exhausted before attempt %d", attempt)
			}
			if attemptTimeout <= 0 || remaining < attemptTimeout {
				attemptTimeout = remaining
			}
		}
		attemptRubric := rubric
		if phase == "repair" || phase == "escalate" {
			attemptRubric = rubric + repairAppendix(lastDraft, lastFindings, phase)
		}
		lastResult = g.Invoke(ctx, InvokeRequest{
			Binding: binding, Section: section, Rubric: attemptRubric,
			Payload: payload,
			Charter: charter,
			Expect:  ExpectPerform, Timeout: attemptTimeout, Runner: runner,
			Sink: sink, OnEvent: onEvent, StoryID: item.ID, Step: toStatus,
			Skill: skill, Actor: "executor",
		})
		totalUsage.Duration += lastResult.Usage.Duration
		if lastResult.Usage.Available {
			totalUsage.Available = true
			totalUsage.InputTokens += lastResult.Usage.InputTokens
			totalUsage.OutputTokens += lastResult.Usage.OutputTokens
			totalUsage.TotalTokens += lastResult.Usage.TotalTokens
		}

		if lastResult.Err != nil {
			g.recordArtifactAttempt(ctx, item.ID, attempt, phase, section, binding, lastResult.Usage,
				[]string{lastResult.Err.Error()}, reason)
			if errors.Is(lastResult.Err, context.Canceled) || errors.Is(lastResult.Err, context.DeadlineExceeded) ||
				errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return withUsage(lastResult, totalUsage), nil, lastResult.Err
			}
			return withUsage(lastResult, totalUsage), nil, lastResult.Err
		}

		candidate, decodeErr := agentartifact.Decode(lastResult.Stdout)
		lastFindings = nil
		if decodeErr != nil {
			if !contract.Required && errors.Is(decodeErr, agentartifact.ErrNoArtifact) {
				g.recordArtifactAttempt(ctx, item.ID, attempt, phase, section, binding, lastResult.Usage, nil, reason)
				return withUsage(lastResult, totalUsage), nil, nil
			}
			lastFindings = []string{decodeErr.Error()}
			lastDraft = boundedDraft(agentcli.SafeText(string(lastResult.Stdout)))
		} else {
			var findings []string
			candidate, findings = agentartifact.ValidateAll(candidate, contract, item.AcceptanceCriteria)
			lastFindings = findings
			lastDraft = boundedDraft(candidate.Body)
			if len(findings) == 0 {
				g.recordArtifactAttempt(ctx, item.ID, attempt, phase, section, binding, lastResult.Usage, nil, reason)
				return withUsage(lastResult, totalUsage), &candidate, nil
			}
		}
		g.recordArtifactAttempt(ctx, item.ID, attempt, phase, section, binding, lastResult.Usage, lastFindings, reason)

		if policy.TimeBudget > 0 && time.Since(started) >= policy.TimeBudget {
			return withUsage(lastResult, totalUsage), nil, fmt.Errorf(
				"structured output attempt time budget exhausted after %d attempt(s): %s",
				attempt, strings.Join(lastFindings, "; "))
		}
		if policy.TokenBudget > 0 && totalUsage.Available && totalUsage.TotalTokens >= policy.TokenBudget {
			return withUsage(lastResult, totalUsage), nil, fmt.Errorf(
				"structured output token budget exhausted after %d attempt(s): %s",
				attempt, strings.Join(lastFindings, "; "))
		}
		if attempt == maxTotal {
			break
		}

		switch {
		case repairUsed < policy.RepairMax:
			repairUsed++
			phase = "repair"
			reason = "validation-failed"
			binding, section, runner, runTimeout = initialBinding, initialSection, initialRunner, timeout
			if policy.RepairEffort != "" {
				binding.Effort = policy.RepairEffort
			}
		case escalateUsed < policy.EscalateMax:
			escalateUsed++
			phase = "escalate"
			if repairUsed > 0 {
				reason = "validation-after-repair"
			} else {
				reason = "validation-no-repair"
			}
			binding, section, runner, runTimeout = initialBinding, initialSection, initialRunner, timeout
			if policy.EscalateBinding != "" {
				var ok bool
				binding, ok = g.namedAgents(policy.EscalateBinding)
				if !ok {
					return withUsage(lastResult, totalUsage), nil, fmt.Errorf(
						"attempt_escalate_binding %q is not defined in .satelle/agents.toml", policy.EscalateBinding)
				}
				section = policy.EscalateBinding
				if config.ResolvedRole(section, binding) == config.RoleReviewer {
					return withUsage(lastResult, totalUsage), nil, fmt.Errorf(
						"attempt_escalate_binding %q has role=reviewer; quality escalation requires a performer", section)
				}
				if !config.GrantsContextChannel(binding.Tools) {
					return withUsage(lastResult, totalUsage), nil, fmt.Errorf(
						"attempt_escalate_binding %q has no read-only context channel", section)
				}
				var err error
				runner, err = g.newRunner(binding.ResolvedInterface(), binding.CommandTemplate())
				if err != nil {
					return withUsage(lastResult, totalUsage), nil, fmt.Errorf(
						"attempt_escalate_binding %q cannot build an isolated runner: %w", section, err)
				}
				if runner == nil {
					return withUsage(lastResult, totalUsage), nil, fmt.Errorf(
						"attempt_escalate_binding %q is in-loop and cannot run an isolated escalation", section)
				}
				runTimeout, err = binding.TimeoutDuration(g.checkTimeout)
				if err != nil {
					return withUsage(lastResult, totalUsage), nil, err
				}
			}
			if policy.EscalateEffort != "" {
				binding.Effort = policy.EscalateEffort
			}
		default:
			attempt = maxTotal
		}
	}

	return withUsage(lastResult, totalUsage), nil, fmt.Errorf(
		"structured output remained invalid after %d bounded attempt(s): %s",
		attemptsRun, strings.Join(lastFindings, "; "))
}

func withUsage(result InvokeResult, usage agentcli.UsageResult) InvokeResult {
	result.Usage = usage
	return result
}

func repairAppendix(draft string, findings []string, phase string) string {
	payload, _ := json.Marshal(map[string]any{
		"draft": draft, "validation_findings": findings,
	})
	return "\n\n## Targeted " + phase + "\n\nCorrect only the supplied draft against the deterministic findings. " +
		`Return one canonical {"artifact":{"name":"…","type":"…","body":"…"}} object and no narration.` +
		"\n\nRepair input:\n```json\n" + string(payload) + "\n```"
}

func boundedDraft(s string) string {
	const max = 64 << 10
	if len(s) > max {
		return s[:max]
	}
	return s
}

func (g *Engine) recordArtifactAttempt(
	ctx context.Context,
	storyID string,
	attempt int,
	phase, section string,
	binding config.AgentBinding,
	usage agentcli.UsageResult,
	findings []string,
	reason string,
) {
	data := map[string]any{
		"attempt": attempt, "phase": phase, "binding": section,
		"model": binding.Model, "effort": binding.Effort,
		"duration_ms":     usage.Duration.Milliseconds(),
		"usage_available": usage.Available,
		"validator_ok":    len(findings) == 0,
	}
	if usage.Available {
		data["tokens_in"] = usage.InputTokens
		data["tokens_out"] = usage.OutputTokens
		data["tokens_total"] = usage.TotalTokens
	}
	if len(findings) > 0 {
		data["validator_findings"] = strings.Join(findings, " | ")
	}
	if reason != "" {
		data["escalation_reason"] = reason
	}
	g.telemetryEvent(ctx, storyID, "executor", "agent-attempt", data)
}
