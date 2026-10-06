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
	// Remote is true when Agent is the step's declared remote_agent.
	Remote bool
	// Note explains a local placement that was declared remote ("" otherwise).
	Note string
}

// Decide places item's performance of step. The step's remote_agent performs it
// only when every condition holds: the step declares one; item is a child of an
// epic whose container declares schedule = "parallel"; item carries none of the
// step's local_tags; and the session is signed in. A declared-remote step that
// fails only the sign-in test falls back to the step's own agent with a note.
func Decide(ctx context.Context, item workitem.Item, step wfdot.State) Decision {
	local := Decision{Agent: step.Agent}
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
