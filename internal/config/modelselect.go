package config

import "strings"

// Model selection source values (sty_7069bced / epic:model-selection order:3)
// — recorded beside the alias and the resolved id (order:1) on every dispatch,
// so the ledger names not just WHAT ran but WHY that model was chosen.
const (
	ModelSourceBinding               = "binding"
	ModelSourceStep                  = "step"
	ModelSourceAgent                 = "agent"
	ModelSourceInheritedOrchestrator = "inherited-orchestrator"
	ModelSourceInheritedInLoop       = "inherited-in-loop"
	ModelSourceCreator               = "creator"
	ModelSourceCLIDefault            = "cli-default"
)

// SessionModel is one session's captured model and the executable that
// produced it. Executable is the cross-provider guard's evidence: an
// inherited or creator model applies only when it matches the dispatch
// binding's own command executable, so a Claude session's model id can never
// reach a Codex/Grok command (sty_7069bced).
type SessionModel struct {
	Model      string
	Executable string
}

// known reports whether s carries a usable model — not empty, and not the
// explicit "unknown" marker a harness that reports no model captures.
func (s SessionModel) known() bool {
	m := strings.TrimSpace(s.Model)
	return m != "" && !strings.EqualFold(m, "unknown")
}

// SelectInput is everything SelectModel needs to resolve one dispatch's model.
// Precedence, first match wins (sty_7069bced):
//
//  1. Binding — the agents.toml binding's own model=.
//  2. DispatchOverride — a step= model or an --model agent flag naming this one
//     dispatch; DispatchSource says which (ModelSourceStep or ModelSourceAgent).
//  3. Inherited — the higher-ranked of Orchestrator and InLoop, cross-provider
//     guarded; the orchestrator wins a tie.
//  4. Creator — the story-creating session's model, same guard.
//  5. Empty — cli-default: the caller drops {model} and the CLI's own default runs.
type SelectInput struct {
	// Binding is the resolved agents.toml binding's model= value.
	Binding string
	// DispatchOverride is a model named for THIS dispatch by the workflow step
	// or the dispatching agent (e.g. `story rework --model`). Empty means no
	// override for this dispatch.
	DispatchOverride string
	// DispatchSource names which tier DispatchOverride came from
	// (ModelSourceStep or ModelSourceAgent). An agent flag beats a step
	// default when both apply — the caller resolves that tie-break before
	// calling SelectModel and passes the winner here.
	DispatchSource string
	Orchestrator   SessionModel
	InLoop         SessionModel
	Creator        SessionModel
	// Ranking is the [models] ranking table (strongest first). An unranked
	// model loses to any ranked one; with neither ranked, the orchestrator
	// wins the inherited tie (it is the live driving session).
	Ranking []string
	// CommandExecutable is the dispatch binding's own command executable
	// (ExecutableToken) — the cross-provider guard's comparison target.
	CommandExecutable string
	// HasModelSlot reports whether the binding's command template carries a
	// {model} placeholder at all (HasModelSlot helper). An inherited or
	// creator model is never applied when there is no slot to fill.
	HasModelSlot bool
}

// SelectModel resolves the model for one dispatch and names why
// (sty_7069bced). It is a pure function: it never mutates its input, and the
// caller applies the result to its own per-dispatch copy of the binding —
// the loaded agents.toml binding set is never mutated.
func SelectModel(in SelectInput) (model, source string) {
	if m := strings.TrimSpace(in.Binding); m != "" {
		return m, ModelSourceBinding
	}
	if m := strings.TrimSpace(in.DispatchOverride); m != "" {
		src := in.DispatchSource
		if src != ModelSourceStep && src != ModelSourceAgent {
			src = ModelSourceAgent
		}
		return m, src
	}
	if m, src, ok := inheritedModel(in); ok {
		return m, src
	}
	if m, ok := crossProviderApplied(in.Creator, in); ok {
		return m, ModelSourceCreator
	}
	return "", ModelSourceCLIDefault
}

// inheritedModel resolves tier 3: the higher-ranked of the orchestrator and
// in-loop session models, both cross-provider guarded. The orchestrator wins
// when only one is guard-eligible, and wins a rank tie when both are.
func inheritedModel(in SelectInput) (model, source string, ok bool) {
	orch, orchOK := crossProviderApplied(in.Orchestrator, in)
	loop, loopOK := crossProviderApplied(in.InLoop, in)
	switch {
	case orchOK && loopOK:
		if higherOrEqual(orch, loop, in.Ranking) {
			return orch, ModelSourceInheritedOrchestrator, true
		}
		return loop, ModelSourceInheritedInLoop, true
	case orchOK:
		return orch, ModelSourceInheritedOrchestrator, true
	case loopOK:
		return loop, ModelSourceInheritedInLoop, true
	default:
		return "", "", false
	}
}

// crossProviderApplied returns s.Model when it is known AND safe to apply to
// this dispatch: the binding's command carries a {model} slot, and s's
// executable matches the binding's own command executable. An inherited or
// creator model that fails either check falls through rather than crossing
// providers (sty_7069bced).
func crossProviderApplied(s SessionModel, in SelectInput) (string, bool) {
	if !s.known() || !in.HasModelSlot {
		return "", false
	}
	if !strings.EqualFold(strings.TrimSpace(s.Executable), strings.TrimSpace(in.CommandExecutable)) {
		return "", false
	}
	return strings.TrimSpace(s.Model), true
}

// higherOrEqual reports whether a should win over b by rank: lower index is
// stronger; an unranked model loses to a ranked one; when neither ranks, a
// wins (callers pass the orchestrator's model as a so it wins the tie).
func higherOrEqual(a, b string, ranking []string) bool {
	ai, aok := rankIndex(a, ranking)
	bi, bok := rankIndex(b, ranking)
	switch {
	case aok && bok:
		return ai <= bi
	case aok:
		return true
	case bok:
		return false
	default:
		return true
	}
}

// rankIndex returns model's position in ranking (0 = strongest), case
// insensitive, or ok=false when model is not listed.
func rankIndex(model string, ranking []string) (int, bool) {
	for i, r := range ranking {
		if strings.EqualFold(strings.TrimSpace(r), strings.TrimSpace(model)) {
			return i, true
		}
	}
	return 0, false
}

// HasModelSlot reports whether command carries a {model} placeholder anywhere
// — an exact argv token ("--model {model}") or fused into one
// ("model={model}", sty_aa726901's fused-placeholder form). Both are
// substituted by agentcli.buildArgs, so both count as a real slot.
func HasModelSlot(command string) bool {
	return strings.Contains(command, "{model}")
}
