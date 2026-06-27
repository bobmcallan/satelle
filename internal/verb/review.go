package verb

import (
	"context"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// GateDecision is an isolated reviewer's verdict on a requested status
// transition. Gated reports whether a reviewer skill governed the edge at all —
// an ungated edge (no reviewer_skill, or its rubric not installed) is advisory
// and enacts directly, preserving the gateless baseline.
type GateDecision struct {
	Gated  bool   // a reviewer skill judged this edge
	Accept bool   // accept enacts the transition; reject blocks it
	Notes  string // reviewer notes — pushback to the executor on reject
	Skill  string // the reviewer skill that judged it
}

// TransitionGater judges a requested status transition in an isolated,
// fresh-context subprocess. The implementation lives in internal/reviewer; verb
// holds only the seam so the dispatch layer stays free of the agent CLI.
type TransitionGater interface {
	Gate(ctx context.Context, item workitem.Item, toStatus string) (GateDecision, error)
}

// transitionGater is wired once at bootstrap (cli/app openAppForCmd). Nil means
// no reviewer is configured — transitions enact directly (advisory).
var transitionGater TransitionGater

// SetTransitionGater wires the reviewer that gates status transitions. Pass nil
// to disable gating (tests / no-reviewer environments).
func SetTransitionGater(g TransitionGater) { transitionGater = g }
