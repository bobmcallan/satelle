// Package ledger is satelle's append-only event log — the "evidence"
// dynamic primitive. It records what happened to stories and tasks (created,
// updated, status changes, comments) as immutable rows.
//
// Ported from satellites' internal/ledger, reimplemented against sqlite
// (modernc.org/sqlite, no cgo) with the Postgres-specific surface dropped:
// no append-only triggers (no UPDATE/DELETE methods exist, so the table is
// append-only by construction), no AppendMany/EngagementSeqExists (server-sync
// concerns, off in the MVP), and `?` placeholders instead of `$N`. SQL is kept
// libSQL-compatible so a future driver swap is mechanical.
package ledger

import (
	"encoding/json"
	"fmt"
	"time"
	"uuid"
)

// Kind discriminators for the canonical entry shapes the work layer emits.
// Callers may append entries of any kind; these are the names satelle uses.
const (
	KindStoryCreated = "story_created"
	KindStoryUpdated = "story_updated"
	KindTaskCreated  = "task_created"
	KindTaskUpdated  = "task_updated"
	KindComment      = "comment"
	// Quality-management spine: a gated transition records the request and its
	// verdict; every enacted status change records a transition. These feed the
	// progress/review-lights column.
	KindStatusTransition = "status_transition"
	// KindStatusReconcile records that a story row's status was repaired from
	// the last status_transition (the row is a projection of the ledger).
	KindStatusReconcile = "status_reconcile"
	KindReviewAccept    = "review_accept"
	KindReviewReject    = "review_reject"
	// KindGateSkipped records that an edge DECLARED a gate whose skill does not
	// resolve, so the transition advanced with no reviewer and no verdict. It is
	// deliberately its own kind: folding it into a comment would bury it, and
	// folding it into an accept would assert a judgement that never happened.
	// A trail carrying this row says "this advance was not judged" (sty_d59ec6a9).
	KindGateSkipped = "gate_skipped"
	// KindStepSummary is the summariser's prose recap of an enacted transition.
	KindStepSummary = "step_summary"
	// KindAgentInvocation records HOW an isolated agent was invoked for a step —
	// the agent role, the resolved command/harness, and the injected-context source
	// (the skill/rubric file) — so the timeline shows what command ran with what
	// context, not just the verdict (sty_fb3e0873).
	KindAgentInvocation = "agent_invocation"
	// Estimate/actual: an agent self-reports a plan estimate at begin-work and the
	// actual cost at close. Recorded as story tags (estimate-*/actual-*) and as
	// these ledger rows so the per-story close-out can compare estimate vs actual.
	KindEstimateRecorded = "estimate_recorded"
	KindActualRecorded   = "actual_recorded"
	// KindStepCost records a single STEP's self-reported actual token cost and/or
	// its per-step estimate (`satelle story step-cost`) — the only way to attribute
	// cost to an IN-LOOP step, whose tokens the CLI (a subprocess of the driving
	// session) cannot introspect. Per-step wall-time is derived from transition
	// timestamps, not stored here. Payload carries numbers + the step name only —
	// never env/secrets (sty_3b2e55f5).
	KindStepCost = "step_cost"
	// KindWorkflowStamped records the workflow chosen to govern a story at create
	// (sty_3800ac23) — the choice satelle's gates read thereafter.
	KindWorkflowStamped = "workflow_stamped"
	// KindTelemetryEvent records a generic, typed quality/telemetry event: an
	// orchestrator self-report (e.g. an in-loop step's cost, retiring the narrow
	// KindStepCost verb) or a dispatch-engine outcome only the binary observes — a
	// reviewer/executor retry, failure, or timeout on a killed/timed-out
	// subprocess. Payload shape: {"kind": "<event-kind>", "data": {...}} — data
	// carries typed fields only, never env/secrets (sty_b73c3236).
	KindTelemetryEvent = "telemetry_event"
	// KindEngagementBaseline records the git HEAD (and dirty-worktree flag) at
	// first entry into a performing/engaging state so gates can enumerate
	// diff-since-engagement (sty_da169e03 / epic:scope-integrity). Enumeration
	// only — no pass/fail in Go.
	KindEngagementBaseline = "engagement_baseline"
	// KindSuiteRun records ONE run of an expensive verification suite as
	// SHA-keyed evidence: {sha, command, outcome, started_at, finished_at} in
	// Payload, StoryID being the recording story (sty_183a0510). Sibling stories
	// cite it rather than re-running the suite. Enumeration only — no pass/fail
	// in Go; a gate's check block decides whether a cited run is acceptable.
	KindSuiteRun = "suite_run"
	// KindSuiteCitation records that a story RIDES a suite_run recorded elsewhere.
	// Refs carries {"suite_run": "<entry id>"} — the first use of Entry.Refs
	// (sty_183a0510). Enumeration only — no pass/fail in Go.
	KindSuiteCitation = "suite_citation"
	// KindChangeRecord records the set of files changed during a closed step at
	// the enacted transition (sty_948ad5df). Payload is paths and counts only —
	// never file content (the patch rides a type:change story attachment, local
	// only). Enumeration only — no pass/fail in Go.
	KindChangeRecord = "change_record"
	// KindDefinitionAmended records a gate-approved amendment of a story's frozen
	// definition fields (sty_81aa4d8f): Payload carries {reason, skill, fields:
	// [{field, old, new}]} so the trail shows WHAT the definition was before the
	// correction, not merely that one happened. Enumeration only — the accept/
	// reject decision belongs to the authored amend_review gate.
	KindDefinitionAmended = "definition_amended"
)

// Entry is one row of the evidence ledger. StoryID/ProjectID are optional
// correlation ids — a row may be scoped to either, both, or neither.
type Entry struct {
	ID        string          `json:"id"`
	StoryID   string          `json:"story_id,omitempty"`
	ProjectID string          `json:"project_id,omitempty"`
	Kind      string          `json:"kind"`
	Actor     string          `json:"actor,omitempty"`
	Body      string          `json:"body,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Refs      json.RawMessage `json:"refs,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// EXEMPTION (sty_7db2ed7d): the ledger's Actor field is the event-AUTHOR (who
// recorded the event — "executor"/"reviewer"), a distinct concept from the workflow
// node's performer keyword that the actor→agent rename targeted. It is persisted
// state — the `actor` SQLite column (internal/ledger/store.go) and JSON tag — read
// by every released binary, so it is deliberately kept as "actor" rather than
// migrated. This is a recorded internal exemption, not an oversight.

// NewID returns a fresh ledger-entry id in the evt_<8hex> form, visually
// distinct from sty_/tsk_ ids in tool output. NewV4 explicitly — see
// workitem.Kind.newID for why a truncated id may not ride on New's current
// algorithm (sty_5515036d).
func NewID() string { return fmt.Sprintf("evt_%s", uuid.NewV4().String()[:8]) }

// AppendInput is the typed shape of one ledger insert. Kind is required; every
// correlation id and payload is optional.
type AppendInput struct {
	StoryID   string
	ProjectID string
	Kind      string
	Actor     string
	Body      string
	Payload   json.RawMessage
	Refs      json.RawMessage
}

// ListFilter parameterises List. At least one selectable field (StoryID,
// ProjectID, Kind) must be set — an unfiltered full-table scan is refused.
type ListFilter struct {
	StoryID   string
	ProjectID string
	Kind      string
	Limit     int // <=0 ⇒ default 200, capped at 2000
}
