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

// CreateDraft is a proposed work item handed to the required-structure reviewer
// before it is persisted.
type CreateDraft struct {
	Kind               string   `json:"kind"`
	Title              string   `json:"title"`
	Body               string   `json:"body,omitempty"`
	AcceptanceCriteria string   `json:"acceptance_criteria,omitempty"`
	Priority           string   `json:"priority,omitempty"`
	Category           string   `json:"category,omitempty"`
	Tags               []string `json:"tags,omitempty"`
}

// CreateReviewer judges a draft work item's required structure before creation,
// in an isolated subprocess. Implemented in internal/reviewer.
type CreateReviewer interface {
	ReviewCreate(ctx context.Context, draft CreateDraft) (GateDecision, error)
}

// createReviewer is wired only when a repo opts into create-gating
// (satelle.toml [review] gate_create). Nil means creation is ungated.
var createReviewer CreateReviewer

// SetCreateReviewer wires the required-structure reviewer. Pass nil to disable.
func SetCreateReviewer(r CreateReviewer) { createReviewer = r }

// StepSummariser produces a read-only prose recap of an enacted transition,
// recorded as a step_summary ledger row. Implemented in internal/reviewer.
type StepSummariser interface {
	Summarise(ctx context.Context, item workitem.Item, from, to string) (string, error)
}

// stepSummariser runs after a gated transition is enacted. Nil disables it.
var stepSummariser StepSummariser

// SetStepSummariser wires the per-transition summariser. Pass nil to disable.
func SetStepSummariser(s StepSummariser) { stepSummariser = s }
