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
	"fmt"
	"strings"

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
	// Pinned is true when the child carries a placement pin that decided this
	// placement (the pin value is then Team, InLoop, or neither for local).
	Pinned bool
	// InLoop is true when the child is pinned in-loop: the driving session
	// performs the step, whatever the binding says.
	InLoop bool
	// PinErr is set when the child carries a placement pin that cannot be
	// honoured (an unknown value, "remote", or two pins that disagree). The
	// caller refuses to resolve the step rather than fall back silently.
	PinErr error
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
//
// A child the orchestrator pinned (a tag under the step's placement_tag prefix,
// written at readiness — sty_c931cd90) is placed by its pin before anything
// else: the pin replaces the binding's own placement and the remote_agent path is
// never reached. A pin never places a child remote; that stays the binding's
// placement = "remote" and the step's remote_agent.
func Decide(ctx context.Context, item workitem.Item, step wfdot.State, bindings Bindings) Decision {
	local := bindingPlacement(step.Agent, bindings)
	if pinned, ok := applyPin(local, item, step); ok {
		return pinned
	}
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

// applyPin places item by its placement pin, over base (the binding's own
// placement). ok is false when the step declares no placement_tag or item carries
// no tag under it, so the caller decides as before. An unparsable or conflicting
// pin returns ok with PinErr set.
func applyPin(base Decision, item workitem.Item, step wfdot.State) (Decision, bool) {
	if step.PlacementTag == "" {
		return base, false
	}
	var value string
	found := false
	for _, tag := range item.Tags {
		v, has := strings.CutPrefix(tag, step.PlacementTag)
		if !has {
			continue
		}
		if found && v != value {
			base.PinErr = fmt.Errorf("%s carries conflicting placement pins %q and %q", item.ID, step.PlacementTag+value, tag)
			return base, true
		}
		value, found = v, true
	}
	if !found {
		return base, false
	}
	kind, path, err := config.ParsePin(value)
	if err != nil {
		base.PinErr = fmt.Errorf("%s: tag %q: %w", item.ID, step.PlacementTag+value, err)
		return base, true
	}
	// The pin replaces the binding's placement: a pinned child is never remote.
	base.Pinned = true
	base.Remote, base.Channel, base.Team, base.InLoop = false, false, "", false
	switch kind {
	case config.PlacementTeam:
		base.Team = path
	case config.PlacementInLoop:
		base.InLoop = true
	}
	return base, true
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
