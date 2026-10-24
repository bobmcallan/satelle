// Package placement decides where a child of an epic runs a step (sty_dde8b6a4).
//
// A spine performer step may declare `remote_agent` (an interface=cloud binding)
// and `local_tags` in step.toml. The rule is mechanism; the binding and the tag
// vocabulary are the repo's configuration, so nothing here names a tag or a
// provider. Agentstep (which dispatches the step) and the rework command (which
// must refuse a relay for remote work) both ask the one function below, so they
// cannot disagree about where a child runs.
package placement

import (
	"context"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// NotSignedIn is the note recorded when a step declared remote placement but the
// session is local-only, so the step's own agent performed it.
const NotSignedIn = "placement: remote declared, local used — not signed in"

// Remote is the value recorded as a cloud dispatch's placement.
const Remote = "remote"

// Decision is where a child runs a step.
type Decision struct {
	// Agent is the agents.toml binding that performs the step.
	Agent string
	// Remote is true when Agent is placed off this machine: the step's declared
	// remote_agent (a cloud session), or a binding with placement = "remote"
	// (Channel).
	Remote bool
	// Channel is true when Agent's binding declares placement = "remote": its
	// performer is an ephemeral remote paired with this session over the remote
	// channel (sty_7028d996). Remote is true with it.
	Channel bool
	// Team is the team remote path when Agent's binding declares placement =
	// "team:<path>": that remote is woken for the story instead of a performer
	// being dispatched (sty_7028d996). Empty otherwise.
	Team string
	// Note explains a local placement that was declared remote ("" otherwise).
	Note string
}

// Bindings resolves a named agents.toml binding. It is how Decide reads a
// binding's placement; nil means no binding declares one.
type Bindings func(name string) (config.AgentBinding, bool)

// Decide places item's performance of step. The step's remote_agent performs it
// only when every condition holds: the step declares one; item is a child of an
// epic whose container is parallel (the default; sequential = true opts out);
// item carries none of the
// step's local_tags; and the session is signed in. A declared-remote step that
// fails only the sign-in test falls back to the step's own agent with a note.
//
// Otherwise the step's own binding declares where it runs (sty_7028d996): a
// binding with placement = "remote" or "team:<path>" is placed accordingly, and
// one declaring none (or local) is placed as before. Placement lives on the
// binding, never in step.toml; remote_agent stays for the cloud path.
func Decide(ctx context.Context, item workitem.Item, step wfdot.State, bindings Bindings) Decision {
	local := bindingPlacement(step.Agent, bindings)
	if step.RemoteAgent == "" {
		return local
	}
	if verb.EpicChildSchedule(ctx, item) != wfdot.SchedParallel {
		return local
	}
	for _, pin := range step.LocalTags {
		for _, tag := range item.Tags {
			if tag == pin {
				return local
			}
		}
	}
	if !verb.SignedIn() {
		local.Note = NotSignedIn
		return local
	}
	return Decision{Agent: step.RemoteAgent, Remote: true}
}

// bindingPlacement places agent by its own binding's declared placement. An
// agent with no performer binding (executor, reviewer, empty, or an unresolved
// name) is the plain local decision: the engine reports an unresolved binding
// itself.
func bindingPlacement(agent string, bindings Bindings) Decision {
	d := Decision{Agent: agent}
	switch agent {
	case "", "executor", "reviewer":
		return d
	}
	if bindings == nil {
		return d
	}
	b, ok := bindings(agent)
	if !ok {
		return d
	}
	switch kind, path := b.ResolvedPlacement(); kind {
	case config.PlacementRemote:
		d.Remote, d.Channel = true, true
	case config.PlacementTeam:
		d.Team = path
	}
	return d
}
