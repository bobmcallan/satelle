package config

import "strings"

// Model selection source values (sty_7069bced / epic:model-selection order:3)
// — recorded beside the alias and the resolved id (order:1) on every dispatch,
// so the ledger names not just WHAT ran but WHY that model was chosen.
const (
	ModelSourceBinding         = "binding"
	ModelSourceStep            = "step"
	ModelSourceAgent           = "agent"
	ModelSourceInheritedInLoop = "inherited-in-loop"
	ModelSourceCreator         = "creator"
	ModelSourceOrder           = "order"
	ModelSourceCLIDefault      = "cli-default"
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
//  3. Inherited — the InLoop session's model, cross-provider guarded.
//  4. Creator — the story-creating session's model, same guard.
//  5. Order — the first entry of the dispatch executable's [model_order] list,
//     when the binding can accept a model. Never another executable's list.
//  6. Empty — cli-default: the caller drops {model} and the CLI's own default runs.
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
	InLoop         SessionModel
	Creator        SessionModel
	// Order is the [model_order] list for CommandExecutable only (the caller
	// looks it up with AgentsConfig.OrderFor); empty means none is configured.
	Order []ModelRank
	// CommandExecutable is the dispatch binding's own command executable
	// (ExecutableToken) — the cross-provider guard's comparison target.
	CommandExecutable string
	// HasModelSlot reports whether the binding's command template carries a
	// {model} placeholder at all (HasModelSlot helper). An inherited or
	// creator model is never applied when there is no slot to fill.
	HasModelSlot bool
	// ModelViaSession reports that the binding's transport applies a model
	// in-protocol (ACP session/set_config_option) rather than through a
	// {model} argv slot, so an inherited or creator model is applicable
	// without one. The executable-match guard still applies.
	ModelViaSession bool
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
	if m, ok := crossProviderApplied(in.InLoop, in); ok {
		return m, ModelSourceInheritedInLoop
	}
	if m, ok := crossProviderApplied(in.Creator, in); ok {
		return m, ModelSourceCreator
	}
	if in.HasModelSlot || in.ModelViaSession {
		for _, r := range in.Order {
			if m := r.First(); m != "" {
				return m, ModelSourceOrder
			}
		}
	}
	return "", ModelSourceCLIDefault
}

// crossProviderApplied returns s.Model when it is known AND safe to apply to
// this dispatch: the binding can accept a model (a {model} slot or an in-protocol session config), and s's
// executable matches the binding's own command executable. An inherited or
// creator model that fails either check falls through rather than crossing
// providers (sty_7069bced).
func crossProviderApplied(s SessionModel, in SelectInput) (string, bool) {
	if !s.known() || (!in.HasModelSlot && !in.ModelViaSession) {
		return "", false
	}
	if !strings.EqualFold(strings.TrimSpace(s.Executable), strings.TrimSpace(in.CommandExecutable)) {
		return "", false
	}
	return strings.TrimSpace(s.Model), true
}

// HasModelSlot reports whether command carries a {model} placeholder anywhere
// — an exact argv token ("--model {model}") or fused into one
// ("model={model}", sty_aa726901's fused-placeholder form). Both are
// substituted by agentcli.buildArgs, so both count as a real slot.
func HasModelSlot(command string) bool {
	return strings.Contains(command, "{model}")
}
