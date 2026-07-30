// Package guard decides whether a source edit is permitted right now.
package guard

import (
	"fmt"

	"example.com/guard/internal/workflow"
)

// Request is one edit-permission question.
type Request struct {
	State string
	// Agent names the dispatched performer, empty for the driving session.
	Agent string
}

// Decision is the guard's answer. A resolution failure must deny closed and say
// how to recover.
type Decision struct {
	Allowed bool
	Reason  string
}

// Decide answers the request from the graph alone.
func Decide(g workflow.Graph, req Request) Decision {
	node, ok := g.Nodes[req.State]
	if !ok {
		return Decision{Allowed: false, Reason: fmt.Sprintf(
			"state %q is not in the workflow — restamp the story or fix the DOT", req.State)}
	}
	if g.Terminal(req.State) {
		return Decision{Allowed: false, Reason: "terminal state: no edits"}
	}
	if !g.PerformingStates()[req.State] {
		return Decision{Allowed: false, Reason: fmt.Sprintf(
			"state %q allocates no performing agent", req.State)}
	}
	if req.Agent != "" && req.Agent != node.Agent {
		return Decision{Allowed: false, Reason: fmt.Sprintf(
			"agent %q is not the performer for %q (that is %q)", req.Agent, req.State, node.Agent)}
	}
	return Decision{Allowed: true, Reason: "performing state"}
}
