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
	Reasoning string            // optional free-form reasoning (verdict contract; may be empty)
	Skill     string            // the deciding reviewer skill
	Reviewers []ReviewerVerdict // per-reviewer verdicts in run order (empty for the legacy single-reviewer path)
	// Command/Context describe an isolated AGENT invocation (LLM reviewer): the
	// resolved harness command and the injected-context source (skill/rubric file).
	// Empty for a deterministic functional-check gate, which invokes no agent
	// (sty_fb3e0873). They mirror the deciding reviewer for the single-reviewer path.
	Command string
	Context string
	Model   string // the reviewer's configured model alias (sty_a699ad14) — for the cost view
	// ModelResolved is the canonical model id the transport actually ran
	// (sty_87b86044), distinct from Model (the configured alias). Models is
	// every model a multi-model invocation reported. Both are ModelUnavailable/
	// nil respectively when the transport reported no model.
	ModelResolved string
	Models        []ModelUsage
	// ModelSource names why ModelResolved was chosen — binding, step, agent,
	// inherited-orchestrator, inherited-in-loop, creator, or cli-default
	// (config.SelectModel, sty_7069bced). Empty for a functional-check gate,
	// which invokes no agent and so selects no model.
	ModelSource string
	// TokensIn/Out/Total and DurationMs are the invocation's cost (sty_a699ad14),
	// recorded on the agent_invocation ledger entry so per-gate cost is auditable.
	// Zero for a functional-check gate or a plain-text harness that emits no usage.
	// UsageAvailable distinguishes a transport-reported zero from unreported
	// usage (sty_56aae77a) — false means the numbers are not measured.
	TokensIn       int
	TokensOut      int
	TokensTotal    int
	DurationMs     int64
	UsageAvailable bool
	// TokensInFresh/TokensCacheWrite/TokensCacheRead split TokensIn into its
	// disjoint components (sty_363eaf55) — the same accounting UsageResult
	// carries. Zero on a transport that reports no cache split.
	TokensInFresh    int
	TokensCacheWrite int
	TokensCacheRead  int
	// SystemPromptBytes/PayloadBytes are the byte lengths of the system prompt
	// and stdin payload satelle sent for this invocation (sty_363eaf55) —
	// lengths only, never content.
	SystemPromptBytes int
	PayloadBytes      int
	// Unresolved names gate skills this edge DECLARED that do not resolve in the
	// substrate. Those gates degrade to advisory — the edge advances with no
	// reviewer and no verdict — which is deliberate, so a fresh repo works before
	// every gate is authored. It is EVIDENCE OF AN UNGATED ADVANCE, never a
	// verdict: an advance recorded with a non-empty Unresolved was not judged,
	// and without it that is indistinguishable from an edge carrying no gate at
	// all (sty_d59ec6a9).
	Unresolved []string
}

// ReviewerVerdict is one reviewer's verdict within a transition's ordered
// review. Order is its position in the run (workflow-named reviewers first,
// then the always-on system layer); System marks a verdict from that layer.
type ReviewerVerdict struct {
	Skill     string `json:"skill"`
	Order     int    `json:"order"`
	Accept    bool   `json:"accept"`
	Notes     string `json:"notes,omitempty"`
	Reasoning string `json:"reasoning,omitempty"` // optional free-form reasoning (verdict contract)
	System    bool   `json:"system,omitempty"`
	// Command/Context name the agent invocation behind an LLM reviewer's verdict —
	// the resolved harness command and the injected skill/rubric file — so the trail
	// records HOW it was judged, not just the outcome. Empty for a functional check.
	Command string `json:"command,omitempty"`
	Context string `json:"context,omitempty"`
	Model   string `json:"model,omitempty"` // reviewer's configured model alias (sty_a699ad14)
	// ModelResolved/Models mirror GateDecision's fields of the same name
	// (sty_87b86044) — stamped directly on this verdict's ledger row so a
	// review_accept/review_reject entry stands alone after compaction, with no
	// join to an agent_invocation row.
	ModelResolved string       `json:"model_resolved,omitempty"`
	Models        []ModelUsage `json:"model_usage,omitempty"`
	// ModelSource mirrors GateDecision.ModelSource (sty_7069bced), stamped
	// directly on this verdict's ledger row for the same reason ModelResolved is.
	ModelSource string `json:"model_source,omitempty"`
	// Token/wall-time cost of this reviewer's invocation (sty_a699ad14), recorded
	// on its agent_invocation entry for the per-gate cost view.
	// UsageAvailable is stamped without omitempty so unreported ≠ measured zero
	// (sty_56aae77a).
	TokensIn       int   `json:"tokens_in,omitempty"`
	TokensOut      int   `json:"tokens_out,omitempty"`
	TokensTotal    int   `json:"tokens_total,omitempty"`
	DurationMs     int64 `json:"duration_ms,omitempty"`
	UsageAvailable bool  `json:"usage_available"`
	// TokensInFresh/TokensCacheWrite/TokensCacheRead split TokensIn (sty_363eaf55).
	TokensInFresh    int `json:"tokens_in_fresh,omitempty"`
	TokensCacheWrite int `json:"tokens_cache_write,omitempty"`
	TokensCacheRead  int `json:"tokens_cache_read,omitempty"`
	// SystemPromptBytes/PayloadBytes are the byte lengths satelle sent — lengths
	// only, never content (sty_363eaf55).
	SystemPromptBytes int `json:"system_prompt_bytes,omitempty"`
	PayloadBytes      int `json:"payload_bytes,omitempty"`
}

// ModelUsage is one model's token/cost entry from a transport's resolved-model
// report (sty_87b86044). verb owns its own copy rather than importing agentcli
// (review.go deliberately keeps this package free of the agent CLI package).
type ModelUsage struct {
	ID        string `json:"id"`
	TokensIn  int    `json:"tokens_in,omitempty"`
	TokensOut int    `json:"tokens_out,omitempty"`
	// TokensCacheWrite/TokensCacheRead are this model's cache components of
	// TokensIn, when the transport reported them per-model (sty_363eaf55).
	TokensCacheWrite int      `json:"tokens_cache_write,omitempty"`
	TokensCacheRead  int      `json:"tokens_cache_read,omitempty"`
	CostUSD          *float64 `json:"cost_usd,omitempty"`
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

// AmendField is one definition field an amendment proposes to change, with the
// value it holds now and the value proposed — the before/after pair the gate
// judges and the ledger records (sty_81aa4d8f).
type AmendField struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

// AmendDraft is a proposed amendment of a story's FROZEN definition fields,
// handed to the amend gate before anything is written. It carries the story as
// it stands (so the reviewer can judge the correction in context), the state the
// story sits in, the per-field before/after set, and the caller's reason.
type AmendDraft struct {
	Item   workitem.Item `json:"item"`
	Status string        `json:"status"`
	Fields []AmendField  `json:"fields"`
	Reason string        `json:"reason"`
}

// AmendReviewer judges an amendment of frozen definition fields, in an isolated
// subprocess, against the skill+agent the active workflow declares for the
// amend_review lifecycle hook. Implemented in internal/agentstep.
//
// A GateDecision with Gated false means the repo declares no amend gate: the
// caller REFUSES the amendment (the freeze holds) rather than allowing it —
// the opposite of create, where an undeclared gate keeps a bare create legal.
type AmendReviewer interface {
	ReviewAmend(ctx context.Context, draft AmendDraft) (GateDecision, error)
}

var amendReviewer AmendReviewer

// SetAmendReviewer wires the amendment gate. Pass nil to disable — with no
// reviewer wired, `story amend` refuses (fail closed).
func SetAmendReviewer(r AmendReviewer) { amendReviewer = r }

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
	// Model is the binding's configured model alias ({model}), recorded on the
	// agent_invocation so the ledger shows WHICH model was asked to run a step —
	// the audit signal for per-step model mixing (e.g. a GLM planner vs an opus
	// in-loop session, sty_5d48317b). The model id is not a secret; the binding's
	// env (endpoint + token) is deliberately NEVER recorded. ModelResolved below
	// is what the transport actually ran.
	Model string `json:"model,omitempty"`
	// ModelResolved/Models mirror GateDecision's fields of the same name
	// (sty_87b86044) — the canonical model id the dispatch actually ran, and
	// every model a multi-model invocation reported.
	ModelResolved string       `json:"model_resolved,omitempty"`
	Models        []ModelUsage `json:"model_usage,omitempty"`
	// ModelSource names why ModelResolved was chosen (config.SelectModel,
	// sty_7069bced) — see GateDecision.ModelSource.
	ModelSource string `json:"model_source,omitempty"`
	// Token/wall-time cost of the dispatch (sty_a699ad14), recorded on the
	// agent_invocation entry. Zero for a plain-text harness with no usage envelope.
	// UsageAvailable false means the tokens were not reported (sty_56aae77a).
	TokensIn       int   `json:"-"`
	TokensOut      int   `json:"-"`
	TokensTotal    int   `json:"-"`
	DurationMs     int64 `json:"-"`
	UsageAvailable bool  `json:"-"`
	// TokensInFresh/TokensCacheWrite/TokensCacheRead split TokensIn (sty_363eaf55).
	TokensInFresh    int `json:"-"`
	TokensCacheWrite int `json:"-"`
	TokensCacheRead  int `json:"-"`
	// SystemPromptBytes/PayloadBytes are the byte lengths satelle sent — lengths
	// only, never content (sty_363eaf55).
	SystemPromptBytes int    `json:"-"`
	PayloadBytes      int    `json:"-"`
	Skill             string `json:"skill,omitempty"`
	// Output is the dispatched agent's captured stdout (sty_890b86cb). For a task
	// EXECUTION run, the verb layer writes it through as an OKF run-output document
	// under the parent task's folder, so a run's evidence is discoverable per task
	// rather than only in the central executor.log.
	Output string `json:"output,omitempty"`
	// ArtifactName/Type identify a Satelle-owned structured step artifact
	// attached before the transition committed.
	ArtifactName string `json:"artifact_name,omitempty"`
	ArtifactType string `json:"artifact_type,omitempty"`
}

// ExecutorDispatcher runs the named isolated agent a workflow node allocates a
// step to (agent=<name> — sty_fd427546): agents.toml defines WHO, the workflow
// The route defines WHERE, the binary only RUNS it. Called after the edge's gates
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
// holds only the seam. modelOverride is `story retrospect --model` (sty_7069bced).
type Retrospector interface {
	Retrospect(ctx context.Context, item workitem.Item, modelOverride string) (DispatchResult, error)
}

var retrospector Retrospector

// SetRetrospector wires the retrospective dispatcher. Pass nil to disable.
func SetRetrospector(r Retrospector) { retrospector = r }

// SummaryResult is the step summariser's read-only recap plus the cost of the
// isolated agent invocation that produced it (sty_a699ad14): Command/Context/
// Model/tokens/duration mirror a reviewer's ReviewerVerdict so the same
// agent_invocation payload shape covers both. Zero-value cost fields mean the
// summariser produced no billable invocation (e.g. it never ran).
type SummaryResult struct {
	Text           string
	Command        string
	Context        string
	Model          string
	ModelResolved  string // resolved model id the summariser actually ran (sty_87b86044)
	Models         []ModelUsage
	ModelSource    string // why ModelResolved was chosen (config.SelectModel, sty_7069bced)
	TokensIn       int
	TokensOut      int
	TokensTotal    int
	DurationMs     int64
	UsageAvailable bool
	// TokensInFresh/TokensCacheWrite/TokensCacheRead split TokensIn (sty_363eaf55).
	TokensInFresh    int
	TokensCacheWrite int
	TokensCacheRead  int
	// SystemPromptBytes/PayloadBytes are the byte lengths satelle sent — lengths
	// only, never content (sty_363eaf55).
	SystemPromptBytes int
	PayloadBytes      int
}

// StepSummariser produces a read-only prose recap of an enacted transition,
// recorded as a step_summary ledger row. Implemented in internal/agentstep.
type StepSummariser interface {
	Summarise(ctx context.Context, item workitem.Item, from, to string) (SummaryResult, error)
	// MandatorySummary reports whether item's active workflow declares a MANDATORY
	// step-summary node — the gate for surfacing a missing-summary gap at done
	// (sty_a1151fb0).
	MandatorySummary(ctx context.Context, item workitem.Item) bool
}

// stepSummariser runs after a gated transition is enacted. Nil disables it.
var stepSummariser StepSummariser

// SetStepSummariser wires the per-transition summariser. Pass nil to disable.
func SetStepSummariser(s StepSummariser) { stepSummariser = s }
