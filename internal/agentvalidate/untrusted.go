package agentvalidate

import (
	"fmt"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

// checkUntrustedSteps judges every seat on a step that declares
// `untrusted_input = true` in step.toml: the step's performer (agent) and the
// reviewers of its entry (reviewer_agent, or each panel seat). The text such a
// step reads comes from an external raiser, so no seat on it may hold a tool that
// executes a command, reaches the network or changes the tree.
//
// Whether a binding is read-only is agentcli.ReadOnlyGrantProblems' answer, and
// whether a performer can still pull its context is config.GrantsContextChannel's
// — this only walks the route and names what each says. Each cause is its own
// finding, naming the binding, the step and the cause.
func checkUntrustedSteps(workflow string, spec wfdot.Spec, agents config.AgentsConfig, r *Report) {
	for _, st := range spec.States {
		if !st.UntrustedInput {
			continue
		}
		judged := map[string]bool{}
		judge := func(role, seat string, performer bool) {
			if judged[role+"/"+seat] {
				return
			}
			judged[role+"/"+seat] = true
			b, ok := untrustedSeatBinding(agents, seat)
			if !ok {
				return // an unresolved seat is reported by the allocation checks
			}
			for _, cause := range untrustedProblems(b, performer) {
				r.record(health.Error(health.IDStepUntrusted, "Seat on an untrusted-input step is not read-only", fmt.Sprintf(
					"workflow %q step %q declares untrusted_input: %s agent=%s: %s", workflow, st.Name, role, seat, cause)).
					About(seat).
					WithRemediation("grant [" + seat + "] only read tools in .satelle/workflows/agents.toml, bind a pi or other read-only seat, or drop untrusted_input from the step"))
			}
		}
		if st.Agent != "" {
			judge("performer", st.Agent, true)
		}
		for _, tr := range spec.Transitions {
			if tr.To != st.Name || (len(tr.Skills) == 0 && tr.Skill == "") {
				continue
			}
			if len(tr.Panel) > 0 {
				for _, seat := range tr.Panel {
					judge("reviewer", reviewerSeat(seat), false)
				}
				continue
			}
			judge("reviewer", reviewerSeat(tr.Agent), false)
		}
	}
}

// reviewerSeat names the binding a reviewer seat resolves to: empty is [reviewer].
func reviewerSeat(seat string) string {
	if seat == "" {
		return "reviewer"
	}
	return seat
}

func untrustedSeatBinding(agents config.AgentsConfig, seat string) (config.AgentBinding, bool) {
	switch seat {
	case "executor":
		return agents.ExecutorBinding(), true
	case "reviewer":
		return agents.ReviewerBinding(), true
	}
	return agents.NamedBinding(seat)
}

// untrustedProblems returns one cause per way the binding is not read-only on an
// untrusted step. A performer whose grant is read-only but leaves it no context
// channel conflicts with config.GrantsContextChannel; that cause replaces the
// general missing-channel finding (performerChannelProblem) for the same
// binding, so a cause is printed once.
func untrustedProblems(b config.AgentBinding, performer bool) []string {
	var out []string
	if b.ResolvedInterface() == config.InterfaceCloud {
		out = append(out, "interface=cloud returns a branch, so it is not held read-only")
	}
	for _, p := range agentcli.ReadOnlyGrantProblems(b.CommandTemplate(), b.ResolvedInterface(), b.Tools, performer) {
		out = append(out, p.Cause)
	}
	if len(out) == 0 && performer && config.NeedsContextChannel(b) && !config.BindingGrantsContextChannel(b) {
		adapter := agentcli.AdapterName(b.CommandTemplate())
		out = append(out, fmt.Sprintf("no read-only context channel for adapter %s: a dispatched performer must pull its story, through the satelle CLI (a shell) or %s, and this grant offers neither", adapter, agentcli.ContextChannelHint(adapter)))
	}
	return out
}
