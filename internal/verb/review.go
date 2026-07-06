package verb

import (
	"context"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// GateDecision is an isolated reviewer's verdict on a requested status
// transition. Gated reports whether a reviewer skill governed the edge at all —
// an ungated edge (no reviewer_skill, or its rubric not installed) is advisory
// and enacts directly, preserving the gateless baseline.
//
// An edge may be judged by MORE THAN ONE reviewer: a transition can name an
// ordered list of reviewers, and an always-on system reviewer layer runs after
// them. Reviewers carries each reviewer's verdict in run order; the top-level
// Accept/Skill/Notes mirror the deciding reviewer (the first reject, or the last
// reviewer when all accept) so single-reviewer callers keep their contract.
type GateDecision struct {
	Gated     bool              // a reviewer skill judged this edge
	Accept    bool              // accept enacts the transition; reject blocks it
	Notes     string            // reviewer notes — pushback to the executor on reject
	Skill     string            // the deciding reviewer skill
	Reviewers []ReviewerVerdict // per-reviewer verdicts in run order (empty for the legacy single-reviewer path)
	// Command/Context describe an isolated AGENT invocation (LLM reviewer): the
	// resolved harness command and the injected-context source (skill/rubric file).
	// Empty for a deterministic functional-check gate, which invokes no agent
	// (sty_fb3e0873). They mirror the deciding reviewer for the single-reviewer path.
	Command string
	Context string
	Model   string // the reviewer's resolved model id (sty_a699ad14) — for the cost view
	// TokensIn/Out/Total and DurationMs are the invocation's cost (sty_a699ad14),
	// recorded on the agent_invocation ledger entry so per-gate cost is auditable.
	// Zero for a functional-check gate or a plain-text harness that emits no usage.
	TokensIn    int
	TokensOut   int
	TokensTotal int
	DurationMs  int64
}

// ReviewerVerdict is one reviewer's verdict within a transition's ordered
// review. Order is its position in the run (workflow-named reviewers first,
// then the always-on system layer); System marks a verdict from that layer.
type ReviewerVerdict struct {
	Skill  string `json:"skill"`
	Order  int    `json:"order"`
	Accept bool   `json:"accept"`
	Notes  string `json:"notes,omitempty"`
	System bool   `json:"system,omitempty"`
	// Command/Context name the agent invocation behind an LLM reviewer's verdict —
	// the resolved harness command and the injected skill/rubric file — so the trail
	// records HOW it was judged, not just the outcome. Empty for a functional check.
	Command string `json:"command,omitempty"`
	Context string `json:"context,omitempty"`
	Model   string `json:"model,omitempty"` // reviewer's resolved model id (sty_a699ad14)
	// Token/wall-time cost of this reviewer's invocation (sty_a699ad14), recorded
	// on its agent_invocation entry for the per-gate cost view.
	TokensIn    int   `json:"tokens_in,omitempty"`
	TokensOut   int   `json:"tokens_out,omitempty"`
	TokensTotal int   `json:"tokens_total,omitempty"`
	DurationMs  int64 `json:"duration_ms,omitempty"`
}

// TransitionGater judges a requested status transition in an isolated,
// fresh-context subprocess. The implementation lives in internal/agentstep; verb
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
// in an isolated subprocess. Implemented in internal/agentstep.
type CreateReviewer interface {
	ReviewCreate(ctx context.Context, draft CreateDraft) (GateDecision, error)
}

// createReviewer is wired only when a repo opts into create-gating
// (satelle.toml [review] gate_create). Nil means creation is ungated.
var createReviewer CreateReviewer

// SetCreateReviewer wires the required-structure reviewer. Pass nil to disable.
func SetCreateReviewer(r CreateReviewer) { createReviewer = r }

// WorkflowResolver names the workflow that governs a story of a given category,
// so the create path can STAMP the choice on the story (sty_3800ac23) and the
// restamp path can re-resolve it mid-flight (sty_ed3386cf). Wired independently
// of create-gating — a story is stamped whenever a workflow governs it.
// Implemented in internal/agentstep.
type WorkflowResolver interface {
	WorkflowNameFor(ctx context.Context, category string) string
	// WorkflowStates returns the lifecycle states the named workflow declares and
	// whether the workflow resolves at all — the restamp validation seam. An empty
	// state list on a resolved workflow means the lifecycle was not statically
	// parseable; the caller skips the status-compatibility check rather than
	// blocking the restamp.
	WorkflowStates(ctx context.Context, name string) ([]string, bool)
}

var workflowResolver WorkflowResolver

// SetWorkflowResolver wires the governing-workflow resolver. Pass nil to disable
// stamping.
func SetWorkflowResolver(r WorkflowResolver) { workflowResolver = r }

// DispatchResult reports a named-agent executor dispatch (sty_fd427546):
// whether the target state was allocated to a named isolated agent and, when it
// was, which agent/harness performed it and under which rubric.
type DispatchResult struct {
	Dispatched bool   `json:"dispatched"`
	Agent      string `json:"agent,omitempty"`
	Command    string `json:"command,omitempty"`
	// Model is the binding's resolved model id ({model}), recorded on the
	// agent_invocation so the ledger shows WHICH model actually ran a step — the
	// audit signal for per-step model mixing (e.g. a GLM planner vs an opus
	// in-loop session, sty_5d48317b). The model id is not a secret; the binding's
	// env (endpoint + token) is deliberately NEVER recorded.
	Model string `json:"model,omitempty"`
	// Token/wall-time cost of the dispatch (sty_a699ad14), recorded on the
	// agent_invocation entry. Zero for a plain-text harness with no usage envelope.
	TokensIn    int    `json:"-"`
	TokensOut   int    `json:"-"`
	TokensTotal int    `json:"-"`
	DurationMs  int64  `json:"-"`
	Skill       string `json:"skill,omitempty"`
	// Output is the dispatched agent's captured stdout (sty_890b86cb). For a task
	// EXECUTION run, the verb layer writes it through as an OKF run-output document
	// under the parent task's folder, so a run's evidence is discoverable per task
	// rather than only in the central executor.log.
	Output string `json:"output,omitempty"`
}

// ExecutorDispatcher runs the named isolated agent a workflow node allocates a
// step to (agent=<name> — sty_fd427546): agents.toml defines WHO, the workflow
// DOT defines WHERE, the binary only RUNS it. Called after the edge's gates
// accept and BEFORE the status is enacted — an error refuses the transition
// (status unchanged). Implemented in internal/agentstep.
type ExecutorDispatcher interface {
	DispatchExecutor(ctx context.Context, item workitem.Item, toStatus string) (DispatchResult, error)
}

// executorDispatcher is wired at bootstrap beside the transition gater. Nil
// keeps every step in-loop (today's behaviour).
var executorDispatcher ExecutorDispatcher

// SetExecutorDispatcher wires the named-agent dispatch. Pass nil to disable.
func SetExecutorDispatcher(d ExecutorDispatcher) { executorDispatcher = d }

// Retrospector dispatches the retrospective agent over a finished story to emit
// improvement proposals (sty_b53730e2). Implemented in internal/agentstep; verb
// holds only the seam.
type Retrospector interface {
	Retrospect(ctx context.Context, item workitem.Item) (DispatchResult, error)
}

var retrospector Retrospector

// SetRetrospector wires the retrospective dispatcher. Pass nil to disable.
func SetRetrospector(r Retrospector) { retrospector = r }

// StepSummariser produces a read-only prose recap of an enacted transition,
// recorded as a step_summary ledger row. Implemented in internal/agentstep.
type StepSummariser interface {
	Summarise(ctx context.Context, item workitem.Item, from, to string) (string, error)
	// MandatorySummary reports whether item's active workflow declares a MANDATORY
	// step-summary node — the gate for surfacing a missing-summary gap at done
	// (sty_a1151fb0).
	MandatorySummary(ctx context.Context, item workitem.Item) bool
}

// stepSummariser runs after a gated transition is enacted. Nil disables it.
var stepSummariser StepSummariser

// SetStepSummariser wires the per-transition summariser. Pass nil to disable.
func SetStepSummariser(s StepSummariser) { stepSummariser = s }
