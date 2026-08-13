package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Story and task are the same primitive (workitem) distinguished by kind, so
// the create/list handlers are produced per-kind by a factory and the
// kind-agnostic get/set handlers are shared. Each is registered under both the
// "story-" and "task-" name prefixes so the CLI groups map 1:1.
func init() {
	for _, kind := range []workitem.Kind{workitem.KindStory, workitem.KindTask, workitem.KindExecution} {
		group := string(kind)
		Register(&Verb{Name: group + "-create", Description: "Create a " + group, Invoke: workItemCreate(kind)})
		Register(&Verb{Name: group + "-list", Description: "List " + group + "s", Invoke: workItemList(kind)})
		Register(&Verb{Name: group + "-get", Description: "Get a " + group + " by id", Invoke: workItemGet})
		Register(&Verb{Name: group + "-set", Description: "Update a " + group, Invoke: workItemSet})
	}
	// Estimate/actual are story-only: an agent records the plan estimate at
	// begin-work and the actual cost at close, scoped to the story.
	Register(&Verb{Name: "story-estimate", Description: "Record a story's plan estimate (time/tokens)", Invoke: storyEstimate})
	Register(&Verb{Name: "story-actual", Description: "Record a story's actual cost (time/tokens)", Invoke: storyActual})
	Register(&Verb{Name: "story-resummarise", Description: "Re-run the step summariser for one edge to close a missing-summary gap", Invoke: storyResummarise})
	Register(&Verb{Name: "story-retrospect", Description: "Run the retrospective agent over a finished story to file improvement proposals", Invoke: storyRetrospect})
	// Restamp is story-only too: tasks/executions are unstamped by design
	// (sty_3800ac23 / sty_ef08ce2a) — they resolve their workflow at gate time.
	Register(&Verb{Name: "story-restamp", Description: "Re-stamp a story's governing workflow (re-resolve by category, or an explicit workflow)", Invoke: storyRestamp})
}

// createReq is the request body for story-create / task-create.
type createReq struct {
	Title              string   `json:"title"`
	Body               string   `json:"body,omitempty"`
	Status             string   `json:"status,omitempty"`
	Priority           string   `json:"priority,omitempty"`
	Category           string   `json:"category,omitempty"`
	ParentID           string   `json:"parent_id,omitempty"`
	AcceptanceCriteria string   `json:"acceptance_criteria,omitempty"`
	Tags               []string `json:"tags,omitempty"`
}

// workflowStamp returns the workflow:<name> stamp carried in tags, or "" when
// un-stamped.
func workflowStamp(tags []string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, "workflow:") {
			return strings.TrimSpace(strings.TrimPrefix(t, "workflow:"))
		}
	}
	return ""
}

// hasWorkflowStamp reports whether tags already carry a workflow:<name> stamp, so
// an explicit caller-supplied workflow tag is never duplicated.
func hasWorkflowStamp(tags []string) bool { return workflowStamp(tags) != "" }

func workItemCreate(kind workitem.Kind) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	ledgerKind := ledger.KindStoryCreated
	if kind == workitem.KindTask || kind == workitem.KindExecution {
		ledgerKind = ledger.KindTaskCreated
	}
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		store, err := requireWorkItem()
		if err != nil {
			return nil, err
		}
		var req createReq
		if err := decode(raw, &req); err != nil {
			return nil, err
		}
		now := time.Now()

		// Controlled tag vocabulary (sty_034d843c): validate + canonicalise BEFORE
		// the create reviewer so a typo fails fast (no subprocess) and the
		// reviewer sees the stored form. Unwired / empty vocab = no-op.
		if canon, cerr := canonicaliseTags(req.Tags); cerr != nil {
			return nil, cerr
		} else {
			req.Tags = canon
		}
		// Category vocabulary (sty_b2315e17): canonical casing always; hard-reject
		// only when enforce=reject. Warn mode is surfaced on the CLI path.
		if canon, cerr := canonicaliseCategory(req.Category); cerr != nil {
			return nil, cerr
		} else {
			req.Category = canon
		}

		// Required-structure gate: when a repo opts in, an isolated reviewer
		// judges the draft before it is persisted (the deterministic
		// structure.Story check — including the category rule, sty_af239840 —
		// runs first inside it). A bare create stays legal by design: stories
		// start as stubs and the WORKFLOW gates enforce structure progressively;
		// `satelle reindex` reports non-conformant open stories as warnings.
		if createReviewer != nil {
			dec, gerr := createReviewer.ReviewCreate(ctx, CreateDraft{
				Kind:               string(kind),
				Title:              req.Title,
				Body:               req.Body,
				AcceptanceCriteria: req.AcceptanceCriteria,
				Priority:           req.Priority,
				Category:           req.Category,
				Tags:               req.Tags,
			})
			if gerr != nil {
				return nil, gerr
			}
			if dec.Gated && !dec.Accept {
				return nil, fmt.Errorf("%s rejected by %s: %s", kind, dec.Skill, dec.Notes)
			}
		}

		// Stamp the GOVERNING workflow on the story at create (sty_3800ac23): the
		// chosen workflow is recorded as a workflow:<name> tag so gating reads the
		// choice thereafter rather than re-deriving it by category. Independent of
		// create-gating — a story is stamped whenever a workflow governs it.
		// Only STORIES are stamped. A task is an authored header with no running
		// lifecycle, and an execution resolves its workflow KIND-awarely at gate
		// time (sty_ef08ce2a) — stamping either risks pinning the wildcard story
		// workflow onto a task/execution, exactly what this epic forbids.
		tags := req.Tags
		stampedWorkflow := ""
		if kind == workitem.KindStory && workflowResolver != nil && !hasWorkflowStamp(tags) {
			if wf := workflowResolver.WorkflowNameFor(ctx, req.Category); wf != "" {
				stampedWorkflow = wf
				tags = append(tags, "workflow:"+wf)
			}
		}

		// Single-story process rule (sty_c7149f8a): refuse creating a story already
		// in an engaging status when another story occupies that seat. Default
		// create status is backlog (not engaging) — no-op unless create opts a
		// non-default status.
		if kind == workitem.KindStory {
			status := req.Status
			if status == "" {
				status = workitem.StatusBacklog
			}
			// ParentID rides along because the seat arbitration key may be
			// derived from it (sty_c098dc2d) — a provisional without it would
			// probe under the wrong key.
			provisional := workitem.Item{
				Kind: workitem.KindStory, Status: status, Category: req.Category, Tags: tags,
				Title: req.Title, ParentID: req.ParentID,
			}
			if err := refuseSecondEngagingStory(ctx, "", status, provisional); err != nil {
				return nil, err
			}
		}

		it, err := store.Create(ctx, workitem.CreateInput{
			Kind:               kind,
			Title:              req.Title,
			Body:               req.Body,
			Status:             req.Status,
			Priority:           req.Priority,
			Category:           req.Category,
			ParentID:           req.ParentID,
			AcceptanceCriteria: req.AcceptanceCriteria,
			Tags:               tags,
			Assignee:           resolveAssignee(),
		}, now)
		if err != nil {
			return nil, err
		}
		// Create-into-engaging: claim the seat now that the id exists (sty_8426b9c0).
		if _, _, aerr := acquireEngagementLease(ctx, it, it.Status); aerr != nil {
			// Best-effort: item was created; surface the seat error so the operator
			// knows the create raced — rare (pre-check already refused when possible).
			return nil, aerr
		}
		appendLedger(ctx, it.ID, ledgerKind, fmt.Sprintf("created %s %q", kind, it.Title), now)
		if stampedWorkflow != "" {
			appendLedger(ctx, it.ID, ledger.KindWorkflowStamped,
				fmt.Sprintf("governing workflow: %s", stampedWorkflow), now)
		}
		// Create-straight-into-engaging: record engagement baseline (sty_da169e03).
		maybeRecordEngagementBaseline(ctx, it, "", it.Status, now)
		appendOpLog(string(kind)+"-create", it.ID,
			fmt.Sprintf("status: %s; tags: [%s]", it.Status, strings.Join(it.Tags, ",")), now)
		// A task/execution is authored substrate — materialise its file (the source
		// of truth): a task header flat, an execution run under its task's folder
		// (sty_c1f9e74c, sty_ef08ce2a). A no-op for stories.
		if err := writeItemFile(it); err != nil {
			return nil, fmt.Errorf("write item file: %w", err)
		}
		notifyChange(panelTopic(kind))
		return json.Marshal(it)
	}
}

// panelTopic maps a work-item kind to its realtime panel topic.
func panelTopic(kind workitem.Kind) string {
	if kind == workitem.KindTask || kind == workitem.KindExecution {
		return TopicTasks
	}
	return TopicStories
}

// listReq is the request body for story-list / task-list.
type listReq struct {
	Status   string `json:"status,omitempty"`
	ParentID string `json:"parent_id,omitempty"`
	Tag      string `json:"tag,omitempty"` // exact tag; multi-value ANY-match (sty_f7115cd2)
	Limit    int    `json:"limit,omitempty"`
}

func workItemList(kind workitem.Kind) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		store, err := requireWorkItem()
		if err != nil {
			return nil, err
		}
		var req listReq
		if err := decode(raw, &req); err != nil {
			return nil, err
		}
		items, err := store.List(ctx, workitem.ListFilter{
			Kind:     kind,
			Status:   req.Status,
			ParentID: req.ParentID,
			Tag:      req.Tag,
			Limit:    req.Limit,
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(items)
	}
}

// idReq is the request body for verbs addressing a single item by id.
type idReq struct {
	ID string `json:"id"`
}

func workItemGet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req idReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, fmt.Errorf("verb: id required")
	}
	it, err := store.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(it)
}

// setReq is the request body for story-set / task-set. Pointer fields give
// partial-update semantics: a field absent from the JSON stays unchanged.
// Tags is full-replace. AddTags/RemoveTags are additive (sty_033d4611) and must
// not be combined with Tags in one call.
type setReq struct {
	ID                 string    `json:"id"`
	Title              *string   `json:"title,omitempty"`
	Body               *string   `json:"body,omitempty"`
	Status             *string   `json:"status,omitempty"`
	Priority           *string   `json:"priority,omitempty"`
	Category           *string   `json:"category,omitempty"`
	ParentID           *string   `json:"parent_id,omitempty"`
	AcceptanceCriteria *string   `json:"acceptance_criteria,omitempty"`
	Tags               *[]string `json:"tags,omitempty"`
	AddTags            []string  `json:"add_tags,omitempty"`
	RemoveTags         []string  `json:"remove_tags,omitempty"`
}

func workItemSet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req setReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, fmt.Errorf("verb: id required")
	}
	now := time.Now()

	// Resolve the current item so we can detect a status transition and gate it
	// before anything is enacted.
	current, err := store.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}

	// Tag mutation algebra (sty_033d4611): full-replace (--tags) is exclusive of
	// additive add/remove. Combined add+remove applies removes first, then adds.
	if req.Tags != nil && (len(req.AddTags) > 0 || len(req.RemoveTags) > 0) {
		return nil, fmt.Errorf("verb: tags (full replace) cannot be combined with add_tags/remove_tags — use one mode")
	}
	if len(req.AddTags) > 0 || len(req.RemoveTags) > 0 {
		merged := applyTagMutation(current.Tags, req.AddTags, req.RemoveTags)
		req.Tags = &merged
	}
	// Controlled tag vocabulary (sty_034d843c): validate the FINAL effective set
	// after add/remove merge or full-replace. Canonicalise so stored casing matches
	// config (exact-equality list --tag keeps working). Unwired / empty = no-op.
	if req.Tags != nil {
		canon, cerr := canonicaliseTags(*req.Tags)
		if cerr != nil {
			return nil, cerr
		}
		req.Tags = &canon
	}
	// Category vocabulary (sty_b2315e17): canonicalise BEFORE definition freeze so
	// a casing-only --category change is not treated as a definition mutation.
	if req.Category != nil {
		canon, cerr := canonicaliseCategory(*req.Category)
		if cerr != nil {
			return nil, cerr
		}
		req.Category = &canon
	}

	// Definition freeze (sty_b572537f): once a STORY leaves its workflow's entry
	// state (Mdiamond / Spec.Start — not a hardcoded "backlog"), title/body/
	// acceptance_criteria/category are immutable. Status, tags, priority,
	// estimate/actual, and attachments still flow. Fail-closed when the entry
	// state cannot be resolved so a broken deployment cannot silently permit
	// an anti-gaming definition edit.
	if current.Kind == workitem.KindStory {
		if frozen := definitionFieldsChanged(current, req); len(frozen) > 0 {
			entry, ok := storyEntryState(ctx, current)
			if !ok {
				return nil, fmt.Errorf(
					"satelle: refusing to change definition fields [%s] on %s — cannot resolve the story's workflow entry state (fix config and retry)",
					strings.Join(frozen, ", "), current.ID)
			}
			if current.Status != entry {
				return nil, fmt.Errorf(
					"satelle: refusing to change frozen definition field(s) [%s] on engaged story %s (status %q; entry state is %q) — title/body/acceptance_criteria/category are immutable once a story leaves its workflow entry state; status/estimate/actual/tags/priority/attachments are unaffected",
					strings.Join(frozen, ", "), current.ID, current.Status, entry)
			}
		}
	}

	transitioning := req.Status != nil && *req.Status != current.Status
	// Same-status re-engage (sty_4f74d01f): when the story is already performing
	// but its seat lease was dropped, `story set --status <same>` must claim a
	// lease (or refuse loudly) — never report plain success with no seat.
	sameStatusReengage := false
	if !transitioning && req.Status != nil && (current.Kind == workitem.KindStory || current.Kind == workitem.KindTask) {
		if eng, ok := storyStatusIsEngaging(ctx, current, *req.Status); ok && eng {
			sameStatusReengage = true
		}
	}

	// Edge fence (sty_ebd3d666): refuse status jumps that skip a required DOT
	// step (e.g. backlog→in_progress when only backlog→plan exists). Runs before
	// engage/lease so a skip is never partially acquired. Declared recovery,
	// park, and cancel edges still pass.
	if transitioning {
		if err := refuseSkippedStep(ctx, current, *req.Status); err != nil {
			return nil, err
		}
	}

	// Engage precondition (sty_93eec36d): when a story leaves its workflow entry
	// state for a non-cancel target, refuse if agents.toml / workflow agent=
	// bindings fail agentvalidate — catch a broken deployment before the first
	// dispatched step, not mid-transition. Cancel stays open.
	if transitioning && current.Kind == workitem.KindStory {
		if err := refuseBrokenAgentsOnEngage(ctx, current, *req.Status); err != nil {
			return nil, err
		}
	}

	// Engagement lease (sty_8426b9c0 / sty_1738f973): acquire the seat BEFORE
	// gate+dispatch so concurrent engages and edit/commit gates see the holder
	// during the whole window. Shape still decides engaging; the lease is WHO
	// holds it. A deferred abort guard releases a NEWLY acquired seat (or clears
	// in_flight on sequential continuation) on ANY return path that does not
	// settle — gate reject, dispatch abort, panic-recoverable exit, Update error.
	// SIGKILL still cannot run defers; HeartbeatTTL + IsStale covers that residual.
	// settled is set only after force-release (exit) or Confirm (engaging commit).
	// sameStatusReengage also acquires (sty_4f74d01f) without running gate/dispatch.
	acquiredThisCall := false
	settled := true // non-transitioning / non-lease paths never need abort release
	if (transitioning || sameStatusReengage) && (current.Kind == workitem.KindStory || current.Kind == workitem.KindTask) {
		// Step-edge stop arbitration (AC5): a pending stop request refuses forward
		// engaging moves; park/terminal remain open.
		if transitioning {
			if err := stopRequestBlocksForward(ctx, current, *req.Status); err != nil {
				return nil, err
			}
		}
		// Assignee holder check (sty_8ccaa906): who may acquire the seat.
		// Before the lease so a refusal never takes it. Non-engaging moves
		// (park, cancel, field edits) are not blocked.
		if eng, ok := storyStatusIsEngaging(ctx, current, *req.Status); ok && eng {
			if err := refuseWrongHolder(current, resolveAssignee()); err != nil {
				return nil, err
			}
		}
		acq, inFlight, aerr := acquireEngagementLease(ctx, current, *req.Status)
		if aerr != nil {
			return nil, aerr
		}
		if sameStatusReengage {
			// Re-seat path: grant lease (or keep existing) and settle immediately —
			// no gate, no dispatch. Other field updates may still apply below.
			if acq {
				confirmEngagementLease(ctx, current.ID, *req.Status)
			}
			// inFlight / AlreadyHeld: seat already held — good enough for AC2.
			// Fall through so title/tags/etc. updates still apply when combined.
			if !transitioning && req.Title == nil && req.Body == nil && req.Priority == nil &&
				req.Category == nil && req.ParentID == nil && req.AcceptanceCriteria == nil &&
				req.Tags == nil {
				// Pure re-engage: return updated record after optional no-op Update.
				it, uerr := store.Update(ctx, req.ID, workitem.UpdateInput{
					Status: req.Status, Assignee: stampEmptyAssignee(ctx, current, req.Status),
				}, now)
				if uerr != nil {
					return nil, uerr
				}
				return json.Marshal(it)
			}
		} else if inFlight {
			// Same-owner already leased at this target (AC3): return current
			// record without re-running gate+dispatch.
			return json.Marshal(current)
		} else {
			acquiredThisCall = acq
			settled = false
		}
	}
	// releaseOnAbort: newly acquired seats are deleted; sequential continuations
	// only clear in_flight so a retry can re-enter (state stays last-committed).
	// Idempotent with Release/ClearInFlight no-ops on a missing row.
	releaseOnAbort := func() {
		if acquiredThisCall {
			releaseEngagementLease(ctx, current.ID)
		} else {
			clearEngagementInFlight(ctx, current.ID)
		}
	}
	defer func() {
		if !settled {
			releaseOnAbort()
		}
	}()

	// Gate the transition through the isolated reviewer, if one is wired and the
	// edge is governed by a reviewer skill. A reject blocks the whole set and
	// pushes the reviewer's notes back to the executor; an ungated edge enacts.
	gatedAccepted := false
	// Verdicts carried past the gate block so the route document can append this
	// step's outcome AFTER the transition commits (sty_39e2d9df). A local, not a
	// signature change: the reasoning belongs on the same artifact as the route.
	var routeVerdicts []ReviewerVerdict
	var routeUnresolved []string
	if transitioning && transitionGater != nil {
		dec, gerr := transitionGater.Gate(ctx, current, *req.Status)
		if gerr != nil {
			return nil, gerr
		}
		// An edge may carry several reviewers (a transition's reviewer list plus
		// the always-on system layer). Record each reviewer's verdict as its own
		// ledger row, in order, so the trail names who judged the edge and how. The
		// single-reviewer path returns no Reviewers — synthesise one from the
		// top-level verdict so both paths record identically.
		reviewers := dec.Reviewers
		if len(reviewers) == 0 && dec.Gated {
			reviewers = []ReviewerVerdict{{Skill: dec.Skill, Accept: dec.Accept, Notes: dec.Notes, Reasoning: dec.Reasoning, Command: dec.Command, Context: dec.Context, Model: dec.Model,
				TokensIn: dec.TokensIn, TokensOut: dec.TokensOut, TokensTotal: dec.TokensTotal, DurationMs: dec.DurationMs,
				UsageAvailable: dec.UsageAvailable}}
		}
		// Ledger ALL verdicts first (accepts and rejects), then refuse if any
		// rejected — so parallel multi-reviewer rounds record every row
		// (sty_4f0a15db AC2). Serial short-circuit still only returns reviewers
		// up to the first reject, so single-reject message format is unchanged.
		var rejects []ReviewerVerdict
		for _, rv := range reviewers {
			// Record HOW the isolated agent was invoked before its verdict — the
			// resolved command/harness and the injected skill/rubric file — so the
			// timeline shows what command ran with what context (sty_fb3e0873). Only
			// an LLM reviewer carries a Command; a functional check invokes no agent.
			if rv.Command != "" {
				appendLedgerEntry(ctx, current.ID, ledger.KindAgentInvocation, "reviewer",
					fmt.Sprintf("invoked reviewer (%s) for %s→%s with @skill:%s", rv.Command, current.Status, *req.Status, rv.Context),
					invocationPayload(current.Status, *req.Status, rv), now)
			}
			if !rv.Accept {
				appendLedgerEntry(ctx, current.ID, ledger.KindReviewReject, "reviewer",
					fmt.Sprintf("rejected %s→%s by %s: %s", current.Status, *req.Status, rv.Skill, rv.Notes),
					reviewerPayload(current.Status, *req.Status, rv), now)
				rejects = append(rejects, rv)
				continue
			}
			acceptBody := fmt.Sprintf("accepted %s→%s by %s: decision=accept notes=%s%s",
				current.Status, *req.Status, rv.Skill, rv.Notes, formatReasoningSuffix(rv.Reasoning))
			appendLedgerEntry(ctx, current.ID, ledger.KindReviewAccept, "reviewer",
				acceptBody, reviewerPayload(current.Status, *req.Status, rv), now)
			// Transparency (design §6.2 / epic AC): surface accept verdicts too —
			// decision, notes, reasoning on stderr (reject already returns as error).
			fmt.Fprintln(os.Stderr, acceptBody)
		}
		if len(rejects) > 0 {
			notifyChange(panelTopic(current.Kind))
			return nil, multiRejectError(current.Status, *req.Status, rejects)
		}
		// An edge may have DECLARED a gate whose skill does not resolve. That gate
		// degraded to advisory and judged nothing, so this advance is ungated —
		// record it as such. Without this row the trail is indistinguishable from
		// an edge that never carried a gate at all, which is the silence
		// sty_d59ec6a9 is about. It does not block: fail-open is deliberate.
		for _, skill := range dec.Unresolved {
			skippedBody := fmt.Sprintf(
				"advanced %s→%s UNGATED — gate skill %q does not resolve in the substrate, so no reviewer judged this transition",
				current.Status, *req.Status, skill)
			appendLedgerEntry(ctx, current.ID, ledger.KindGateSkipped, "reviewer",
				skippedBody, transitionPayload(current.Status, *req.Status, skill), now)
			fmt.Fprintln(os.Stderr, skippedBody)
		}
		if len(reviewers) > 0 {
			gatedAccepted = true
		}
		routeVerdicts, routeUnresolved = reviewers, dec.Unresolved
	}

	// A workflow node may allocate the TARGET state to a NAMED isolated agent
	// (agent=<name> — sty_fd427546). The binding's harness performs the step
	// synchronously here, after the edge's gates accepted and BEFORE the status is
	// enacted: a dispatch failure refuses the whole transition (status unchanged),
	// and the spawned agent never advances status itself — the state's exit gate
	// still governs the next edge. agent=executor and agent-less states dispatch
	// nothing (the in-loop orchestrator performs, today's behaviour).
	if transitioning && executorDispatcher != nil {
		res, derr := executorDispatcher.DispatchExecutor(ctx, current, *req.Status)
		if derr != nil {
			appendLedgerEntry(ctx, current.ID, ledger.KindAgentInvocation, "executor",
				fmt.Sprintf("named-agent dispatch failed for %s→%s: %v", current.Status, *req.Status, derr),
				transitionPayload(current.Status, *req.Status, res.Skill), now)
			notifyChange(panelTopic(current.Kind))
			return nil, fmt.Errorf("transition %s→%s refused: %v", current.Status, *req.Status, derr)
		}
		if res.Dispatched {
			// Record the resolved model (when the binding pins one) so the ledger shows
			// WHICH model ran the step — the audit trail for per-step model mixing
			// (sty_5d48317b). The env (endpoint + token) is never recorded.
			modelNote := ""
			if res.Model != "" {
				modelNote = " on model " + res.Model
			}
			appendLedgerEntry(ctx, current.ID, ledger.KindAgentInvocation, "executor",
				fmt.Sprintf("dispatched step %q to named agent %q%s (%s) with @skill:%s",
					*req.Status, res.Agent, modelNote, res.Command, res.Skill),
				dispatchPayload(current.Status, *req.Status, res), now)
			// A dispatched task-execution run's captured output is written through as
			// an OKF run-output doc under its task folder (sty_890b86cb) — the same
			// write seam the in-loop execution-record verb uses. Best-effort: a write
			// failure is recorded, not fatal (the run already happened).
			if current.Kind == workitem.KindExecution && res.Output != "" {
				if _, werr := recordRunOutput(current, res.Output, now); werr != nil {
					appendLedgerEntry(ctx, current.ID, ledger.KindAgentInvocation, "executor",
						"run-output record failed: "+werr.Error(),
						transitionPayload(current.Status, *req.Status, res.Skill), now)
				}
			}
		}
	}

	// Park origin (sty_f75286dc): stamp authoritative resume-to origin when
	// entering a park node; clear on leave. Not ledger-derived.
	var parkOrigin *string
	if transitioning {
		parkOrigin = parkOriginForTransition(ctx, current, *req.Status)
	}
	it, err := store.Update(ctx, req.ID, workitem.UpdateInput{
		Title:              req.Title,
		Body:               req.Body,
		Status:             req.Status,
		Priority:           req.Priority,
		Category:           req.Category,
		ParentID:           req.ParentID,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Tags:               req.Tags,
		ParkOrigin:         parkOrigin,
		Assignee:           stampEmptyAssignee(ctx, current, req.Status),
	}, now)
	if err != nil {
		return nil, err
	}
	// Release the seat on transition into terminal (Msquare) or park (agent=reviewer).
	// Force-release: exit is config-driven and must free the seat even when the
	// exit CLI process is not the acquire-time owner (default owner is stable
	// per host, but ForceRelease covers any residual owner mismatch).
	if transitioning && targetIsExitState(ctx, it, *req.Status) {
		forceReleaseEngagementLease(ctx, it.ID)
		settled = true
	} else if transitioning {
		// Engaging (or other non-exit) commit: settle lease state + clear in_flight.
		engaging, ok := storyStatusIsEngaging(ctx, it, *req.Status)
		if ok && engaging {
			confirmEngagementLease(ctx, it.ID, *req.Status)
			settled = true
		} else {
			// Non-engaging, non-exit target after a lease was claimed for an
			// engaging path should not happen; treat as settled so we do not
			// roll back a committed status.
			settled = true
		}
	} else {
		settled = true
	}
	if transitioning {
		// An enacted status change records a transition row (feeds the progress
		// column), regardless of whether the edge was gated.
		appendLedgerEntry(ctx, it.ID, ledger.KindStatusTransition, "executor",
			fmt.Sprintf("%s → %s", current.Status, *req.Status),
			transitionPayload(current.Status, *req.Status, ""), now)
		// First entry into an engaging state: record engagement baseline once
		// (sty_da169e03). Enumerates git HEAD for later satelle story diff;
		// never a verdict. Idempotent across park/resume.
		maybeRecordEngagementBaseline(ctx, it, current.Status, *req.Status, now)
		// Record the change set for the step just closed (sty_948ad5df).
		// Enumeration only; best-effort; never blocks the transition.
		recordChangeSet(ctx, it, current.Status, *req.Status, now)
		// Write the route forward (sty_39e2d9df): re-render the plan half so the
		// "you are here" marker is current, and APPEND this step's verdicts and
		// reviewer reasoning to the same artifact. Best-effort, like the change set.
		recordRoute(ctx, it, current.Status, *req.Status, routeVerdicts, routeUnresolved, now)
		// After a GATED transition is enacted, the read-only summariser recaps the
		// step into a step_summary row — but ONLY where the active workflow declares
		// a step-summary node (transparent opt-in; sty_9a139c78). The transition
		// already committed, so a mandatory-summary failure is surfaced on the
		// ledger (it records the gap, it does not revert the step).
		if gatedAccepted && stepSummariser != nil {
			result, serr := stepSummariser.Summarise(ctx, it, current.Status, *req.Status)
			recordStepSummary(ctx, it, current.Status, *req.Status, result, serr, now)
		}
		// On reaching the terminal state, surface (NON-BLOCKING) any mandatory
		// step-summary that never produced its doc — a silent hole in the pull-context
		// chain a dispatched coder reads (sty_a1151fb0). The transition still stands;
		// the gap is made loud + fixable, not reverted.
		if *req.Status == "done" && stepSummariser != nil && stepSummariser.MandatorySummary(ctx, it) {
			surfaceMissingSummaries(ctx, it, now)
		}
	} else {
		ledgerKind := ledger.KindStoryUpdated
		if it.Kind == workitem.KindTask || it.Kind == workitem.KindExecution {
			ledgerKind = ledger.KindTaskUpdated
		}
		appendLedger(ctx, it.ID, ledgerKind, fmt.Sprintf("updated %s", it.Kind), now)
	}
	// Mirror the mutation to the flat operation log (sty_be257fef): the status
	// transition and/or the tag before/after, so a read-only reviewer can verify a
	// DB change (a sprint/order reconciliation, a status move) from a file.
	detail := ""
	if transitioning {
		detail = fmt.Sprintf("status: %s -> %s", current.Status, *req.Status)
	}
	if td := tagsChanged(current.Tags, it.Tags); td != "" {
		if detail != "" {
			detail += "; "
		}
		detail += td
	}
	if detail != "" {
		appendOpLog(string(it.Kind)+"-set", it.ID, detail, now)
	}
	// Keep the task/execution file (the source of truth) current; a no-op for
	// stories (sty_c1f9e74c, sty_ef08ce2a).
	if err := writeItemFile(it); err != nil {
		return nil, fmt.Errorf("write item file: %w", err)
	}
	notifyChange(panelTopic(it.Kind))
	return json.Marshal(it)
}

// restampReq is the request body for story-restamp: the story id and an optional
// explicit workflow name (empty re-resolves from the story's current category).
type restampReq struct {
	ID       string `json:"id"`
	Workflow string `json:"workflow,omitempty"`
}

// storyRestamp re-stamps the governing workflow on an existing story
// (sty_ed3386cf) — the first-class replacement for a hand-edited tag list. It
// re-resolves from the story's CURRENT category through the same seam create
// uses (or stamps an explicit workflow), validates the target before enacting
// (the workflow must resolve, and the story's current status must be a state it
// declares), upserts the workflow: tag leaving every other tag intact, and
// records the change on the ledger and the operation log. Stories only: a task
// header has no running lifecycle and an execution resolves kind-awarely at gate
// time — stamping either is exactly what the stamp design forbids.
func storyRestamp(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req restampReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, fmt.Errorf("verb: id required")
	}
	current, err := store.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if current.Kind != workitem.KindStory {
		return nil, fmt.Errorf("verb: restamp is story-only — a %s is not stamped (it resolves its workflow kind-awarely at gate time)", current.Kind)
	}
	if workflowResolver == nil {
		return nil, fmt.Errorf("verb: no workflow resolver wired — cannot restamp")
	}
	target := req.Workflow
	if target == "" {
		if target = workflowResolver.WorkflowNameFor(ctx, current.Category); target == "" {
			return nil, fmt.Errorf("verb: no workflow governs category %q — pass --workflow", current.Category)
		}
	}
	states, resolved := workflowResolver.WorkflowStates(ctx, target)
	if !resolved {
		return nil, fmt.Errorf("verb: workflow %q does not resolve in the substrate", target)
	}
	if len(states) > 0 && !containsState(states, current.Status) {
		return nil, fmt.Errorf("verb: story status %q is not a state of workflow %q (states: %s) — re-stamp at a compatible point",
			current.Status, target, strings.Join(states, ", "))
	}
	old := workflowStamp(current.Tags)
	if old == target {
		return json.Marshal(current) // already governed by the target — nothing to enact
	}
	merged := upsertKeyedTags(current.Tags, map[string]string{"workflow": target})
	now := time.Now()
	it, err := store.Update(ctx, req.ID, workitem.UpdateInput{Tags: &merged}, now)
	if err != nil {
		return nil, err
	}
	oldLabel := old
	if oldLabel == "" {
		oldLabel = "(unstamped)"
	}
	appendLedger(ctx, it.ID, ledger.KindWorkflowStamped,
		fmt.Sprintf("governing workflow re-stamped: %s -> %s", oldLabel, target), now)
	if td := tagsChanged(current.Tags, it.Tags); td != "" {
		appendOpLog("story-restamp", it.ID, td, now)
	}
	notifyChange(panelTopic(it.Kind))
	return json.Marshal(it)
}

// containsState reports whether states holds s.
func containsState(states []string, s string) bool {
	for _, st := range states {
		if st == s {
			return true
		}
	}
	return false
}

// estimateReq is the request body for story-estimate / story-actual: a token
// count and/or a duration string (e.g. "30m", "2h"), with an optional basis note
// recorded only on the ledger row.
type estimateReq struct {
	ID     string `json:"id"`
	Time   string `json:"time,omitempty"`
	Tokens int    `json:"tokens,omitempty"`
	Basis  string `json:"basis,omitempty"`
}

// storyEstimate records a story's plan estimate as estimate-minutes/estimate-tokens
// tags; storyActual records the actual as actual-minutes/actual-tokens. Both
// preserve the story's other tags and append a ledger row so the close-out can
// compare estimate vs actual.
func storyEstimate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return recordCost(ctx, raw, "estimate", ledger.KindEstimateRecorded)
}

func storyActual(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return recordCost(ctx, raw, "actual", ledger.KindActualRecorded)
}

// recordStepSummary writes the outcome of a step-summary run to the ledger, and on
// success deposits the step-summary-<from>-<to> DOC the pull-context chain reads
// (sty_47d31300). A failure records the gap only — an enacted transition is never
// reverted for a best-effort recap. Shared by the post-transition summariser and
// `satelle story resummarise` (sty_a1151fb0), so the write path is single-sourced.
// When result carries a billable invocation (Command/TokensTotal set), an
// agent_invocation row is also written so the summariser's own cost — a
// documented gap (sty_a699ad14) — folds into `satelle story cost` (sty_b73c3236).
func recordStepSummary(ctx context.Context, item workitem.Item, from, to string, result SummaryResult, serr error, now time.Time) {
	switch {
	case serr != nil:
		appendLedgerEntry(ctx, item.ID, ledger.KindStepSummary, "reviewer",
			"step summary failed: "+serr.Error(),
			transitionPayload(from, to, ""), now)
	case result.Text != "":
		appendLedgerEntry(ctx, item.ID, ledger.KindStepSummary, "reviewer", result.Text,
			transitionPayload(from, to, ""), now)
		if result.Command != "" {
			appendLedgerEntry(ctx, item.ID, ledger.KindAgentInvocation, "reviewer",
				fmt.Sprintf("invoked summariser (%s) for %s→%s with @skill:%s", result.Command, from, to, result.Context),
				summariserInvocationPayload(from, to, result), now)
		}
		if _, _, derr := writeAttachedDoc(ctx, item,
			fmt.Sprintf("step-summary-%s-%s", from, to), "step-summary", result.Text, now); derr != nil {
			appendLedgerEntry(ctx, item.ID, ledger.KindStepSummary, "reviewer",
				"step summary doc deposit failed: "+derr.Error(),
				transitionPayload(from, to, ""), now)
		}
	}
}

// stepSummaryDocExists reports whether the step-summary-<from>-<to> doc is present
// on disk — the pull-context artifact, distinct from the ledger row (a killed run
// writes a "failed" row but NO doc).
func stepSummaryDocExists(item workitem.Item, from, to string) bool {
	dir := attachmentDir(item)
	if dir == "" {
		return false
	}
	file := safeName(fmt.Sprintf("step-summary-%s-%s", from, to))
	_, err := os.Stat(filepath.Join(dir, file))
	return err == nil
}

// surfaceMissingSummaries records a NON-BLOCKING warning when a story reaches done
// with a step-summary that was attempted (a KindStepSummary ledger row exists for the
// edge) but whose DOC is absent — a hole in the pull-context chain (sty_a1151fb0).
// Each gap names its edge and the exact remediation that re-runs just that summary.
func surfaceMissingSummaries(ctx context.Context, item workitem.Item, now time.Time) {
	ls, err := requireLedger()
	if err != nil {
		return
	}
	entries, err := ls.ListByStory(ctx, item.ID, ledger.KindStepSummary)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	var gaps [][2]string
	for _, e := range entries {
		var p struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if json.Unmarshal(e.Payload, &p) != nil || p.From == "" || p.To == "" {
			continue
		}
		key := p.From + "\x00" + p.To
		if seen[key] {
			continue
		}
		seen[key] = true
		if !stepSummaryDocExists(item, p.From, p.To) {
			gaps = append(gaps, [2]string{p.From, p.To})
		}
	}
	if len(gaps) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s reached done with %d missing step-summary doc(s) — the pull-context chain is holed. Re-run each:", item.ID, len(gaps)))
	for _, g := range gaps {
		b.WriteString(fmt.Sprintf("\n  satelle story resummarise %s --from %s --to %s", item.ID, g[0], g[1]))
	}
	appendLedgerEntry(ctx, item.ID, ledger.KindStepSummary, "executor", b.String(), nil, now)
	appendOpLog("story-summary-gap", item.ID, b.String(), now)
	notifyChange(panelTopic(item.Kind))
}

// storyResummarise re-runs the step summariser for exactly one edge and records the
// result (the remediation surfaceMissingSummaries names). It closes a hole left by a
// transient summariser kill without re-driving the whole transition (sty_a1151fb0).
func storyResummarise(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if stepSummariser == nil {
		return nil, fmt.Errorf("verb: no step summariser is wired")
	}
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req struct {
		ID   string `json:"id"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.ID == "" || req.From == "" || req.To == "" {
		return nil, fmt.Errorf("verb: resummarise requires id, --from, and --to")
	}
	it, err := store.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	result, serr := stepSummariser.Summarise(ctx, it, req.From, req.To)
	now := time.Now()
	recordStepSummary(ctx, it, req.From, req.To, result, serr, now)
	if serr != nil {
		return nil, fmt.Errorf("resummarise %s→%s failed: %w", req.From, req.To, serr)
	}
	if result.Text == "" {
		return nil, fmt.Errorf("resummarise %s→%s produced no summary (the workflow may not declare a step-summary node)", req.From, req.To)
	}
	return json.Marshal(map[string]any{"story_id": it.ID, "from": req.From, "to": req.To, "resummarised": true})
}

// storyRetrospect dispatches the retrospective agent over a finished story
// (sty_b53730e2): it reads the story + its plan/summary/ledger and files 1–3
// improvement PROPOSALS as backlog stories. The dispatch's token/wall-time cost is
// recorded on an agent_invocation entry so `satelle story cost` rolls it up like
// any other gate/step.
func storyRetrospect(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if retrospector == nil {
		return nil, fmt.Errorf("verb: no retrospective agent is wired")
	}
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req idReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, fmt.Errorf("verb: id required")
	}
	it, err := store.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	res, rerr := retrospector.Retrospect(ctx, it)
	now := time.Now()
	if res.Dispatched {
		// Record the dispatch's cost/model on an agent_invocation entry, so the
		// retrospective shows in `satelle story cost` beside the gates it reviews.
		appendLedgerEntry(ctx, it.ID, ledger.KindAgentInvocation, "executor",
			fmt.Sprintf("retrospective on %s (%s) with @skill:%s", it.ID, res.Model, res.Skill),
			dispatchPayload("done", "retrospect", res), now)
	}
	if rerr != nil {
		return nil, rerr
	}
	// Omit token numbers when usage was not reported — a bare 0 looks measured
	// (sty_56aae77a). Always stamp usage_available so clients can distinguish.
	out := map[string]any{
		"story_id":        it.ID,
		"output":          res.Output,
		"duration_ms":     res.DurationMs,
		"usage_available": res.UsageAvailable,
	}
	if res.UsageAvailable {
		out["tokens_total"] = res.TokensTotal
	}
	return json.Marshal(out)
}

// recordCost upserts the prefix-minutes/prefix-tokens tags on a story (prefix is
// "estimate" or "actual"), leaving every other tag intact, and records the
// change on the ledger. At least one of tokens/time must be given.
func recordCost(ctx context.Context, raw json.RawMessage, prefix, kind string) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req estimateReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, fmt.Errorf("verb: id required")
	}
	if req.Tokens <= 0 && req.Time == "" {
		return nil, fmt.Errorf("verb: %s requires --tokens and/or --time", prefix)
	}
	current, err := store.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	kv := map[string]string{}
	if req.Tokens > 0 {
		kv[prefix+"-tokens"] = strconv.Itoa(req.Tokens)
	}
	if req.Time != "" {
		d, perr := time.ParseDuration(req.Time)
		if perr != nil {
			return nil, fmt.Errorf("verb: invalid --time %q: %w", req.Time, perr)
		}
		kv[prefix+"-minutes"] = strconv.Itoa(int(d.Minutes()))
	}
	merged := upsertKeyedTags(current.Tags, kv)
	now := time.Now()
	it, err := store.Update(ctx, req.ID, workitem.UpdateInput{Tags: &merged}, now)
	if err != nil {
		return nil, err
	}
	body := fmt.Sprintf("%s recorded: %s", prefix, joinKeyedTags(kv))
	if req.Basis != "" {
		body += " (basis: " + req.Basis + ")"
	}
	appendLedger(ctx, it.ID, kind, body, now)
	appendOpLog("story-"+prefix, it.ID, body, now)
	notifyChange(panelTopic(it.Kind))
	return json.Marshal(it)
}

// applyTagMutation applies remove then add against existing tags (sty_033d4611).
// Remove entries may be exact tags or a namespace group: "sprint:" or "sprint:*"
// drops every tag with that axis. Adds that already exist are no-ops (set
// semantics). Order: survivors keep relative order; new adds append in the
// order given. Result is duplicate-free.
func applyTagMutation(existing, add, remove []string) []string {
	// Build remove predicates.
	exact := map[string]bool{}
	var axes []string // namespace axes to strip entirely
	for _, r := range remove {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if strings.HasSuffix(r, ":*") {
			axes = append(axes, strings.TrimSuffix(r, ":*"))
			continue
		}
		if strings.HasSuffix(r, ":") && !strings.Contains(r[:len(r)-1], ":") {
			// "sprint:" with empty value → group remove
			axes = append(axes, strings.TrimSuffix(r, ":"))
			continue
		}
		exact[r] = true
	}
	dropAxis := func(tag string) bool {
		axis, _, ok := strings.Cut(tag, ":")
		if !ok {
			return false
		}
		for _, a := range axes {
			if a == axis {
				return true
			}
		}
		return false
	}

	out := make([]string, 0, len(existing)+len(add))
	seen := map[string]bool{}
	for _, t := range existing {
		if exact[t] || dropAxis(t) {
			continue
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	for _, t := range add {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// upsertKeyedTags returns existing with any tag whose `key:` matches a key in kv
// removed, then the kv pairs appended in key order — so re-recording an estimate
// replaces the old value rather than duplicating it, and unrelated tags survive.
func upsertKeyedTags(existing []string, kv map[string]string) []string {
	out := make([]string, 0, len(existing)+len(kv))
	for _, t := range existing {
		key := t
		if i := strings.IndexByte(t, ':'); i >= 0 {
			key = t[:i]
		}
		if _, replacing := kv[key]; replacing {
			continue
		}
		out = append(out, t)
	}
	out = append(out, joinedKeys(kv)...)
	return out
}

// joinedKeys renders kv as sorted "key:value" tags.
func joinedKeys(kv map[string]string) []string {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(kv))
	for _, k := range keys {
		out = append(out, k+":"+kv[k])
	}
	return out
}

// joinKeyedTags renders kv as a comma-separated "key:value" list for a ledger body.
func joinKeyedTags(kv map[string]string) string {
	return strings.Join(joinedKeys(kv), ", ")
}

// appendLedger records a work-item lifecycle event, best-effort: a ledger
// failure must not fail the work mutation that already committed. Skipped when
// no ledger store is wired.
func appendLedger(ctx context.Context, storyID, kind, body string, now time.Time) {
	appendLedgerEntry(ctx, storyID, kind, "", body, nil, now)
}

// appendLedgerEntry is appendLedger with an actor and structured payload — used
// by the transition gate (review verdicts, status transitions). Best-effort.
func appendLedgerEntry(ctx context.Context, storyID, kind, actor, body string, payload json.RawMessage, now time.Time) {
	if ledgerStore == nil {
		return
	}
	_, _ = ledgerStore.Append(ctx, ledger.AppendInput{
		StoryID: storyID,
		Kind:    kind,
		Actor:   actor,
		Body:    body,
		Payload: payload,
	}, now)
}

// appendOpLog mirrors a state-mutating verb to the flat-file operation log a
// read-only reviewer can Read/Grep (sty_be257fef). Nil-safe via the Logger;
// records METADATA only (op, ids, before/after of changed fields) — never bodies.
func appendOpLog(op, storyID, detail string, now time.Time) {
	opLog.Append(now, "executor", op, storyID, detail)
}

// definitionFieldsChanged lists which of the four definition fields the request
// actually changes (non-nil pointer whose value differs from current). Order is
// fixed: title, body, acceptance_criteria, category. A resubmitted-identical
// value does not count as a change.
func definitionFieldsChanged(current workitem.Item, req setReq) []string {
	var out []string
	if req.Title != nil && *req.Title != current.Title {
		out = append(out, "title")
	}
	if req.Body != nil && *req.Body != current.Body {
		out = append(out, "body")
	}
	if req.AcceptanceCriteria != nil && *req.AcceptanceCriteria != current.AcceptanceCriteria {
		out = append(out, "acceptance_criteria")
	}
	if req.Category != nil && *req.Category != current.Category {
		out = append(out, "category")
	}
	return out
}

// storyEntryState returns the governing workflow's entry state (Spec.Start —
// conventionally the Mdiamond node) for item. ok is false when the doc index is
// unwired, the workflow list fails, no workflow governs the item, the body is
// not DOT, or Start is empty. Pure governance path via wfgovern + wfdot so the
// freeze holds even with no agent CLI configured.
func storyEntryState(ctx context.Context, item workitem.Item) (string, bool) {
	idx, err := requireDocIndex()
	if err != nil {
		return "", false
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return "", false
	}
	spec, _, _, serr := wfgovern.SpecFor(wfs, item)
	ok := serr == nil
	if !ok {
		return "", false
	}
	start := spec.Start()
	if start == "" {
		return "", false
	}
	return start, true
}

// tagsChanged reports the before/after tag sets as a one-line detail when they
// differ, else "" — so a tag reconciliation (sprint/order) is greppable in the
// operation log. Order-insensitive comparison; the rendered lists keep input order.
func tagsChanged(before, after []string) string {
	if equalStringSet(before, after) {
		return ""
	}
	return fmt.Sprintf("tags: [%s] -> [%s]", strings.Join(before, ","), strings.Join(after, ","))
}

// equalStringSet reports whether two tag slices hold the same set of values.
func equalStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

// transitionPayload is the {from,to,skill} JSON stamped on review/transition
// ledger rows so the progress column can reconstruct the workflow trail.
func transitionPayload(from, to, skill string) json.RawMessage {
	p := struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Skill string `json:"skill,omitempty"`
	}{From: from, To: to, Skill: skill}
	b, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	return b
}

// reviewerPayload is transitionPayload enriched with a single reviewer's order
// and system-layer flag — stamped on each per-reviewer review row so the trail
// preserves who judged the edge, in what order, and whether from the always-on
// system layer.
func reviewerPayload(from, to string, rv ReviewerVerdict) json.RawMessage {
	p := struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Skill     string `json:"skill,omitempty"`
		Order     int    `json:"order"`
		System    bool   `json:"system,omitempty"`
		Notes     string `json:"notes,omitempty"`
		Reasoning string `json:"reasoning,omitempty"`
		Accept    bool   `json:"accept"`
	}{From: from, To: to, Skill: rv.Skill, Order: rv.Order, System: rv.System,
		Notes: rv.Notes, Reasoning: rv.Reasoning, Accept: rv.Accept}
	b, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	return b
}

// dispatchPayload stamps a named-executor agent_invocation row with its model and
// token/wall-time cost (sty_a699ad14), so the per-gate cost view can roll up a
// dispatched step (planner, coder) the same way it does a reviewer gate.
func dispatchPayload(from, to string, res DispatchResult) json.RawMessage {
	p := struct {
		From           string `json:"from"`
		To             string `json:"to"`
		Agent          string `json:"agent"`
		Skill          string `json:"skill,omitempty"`
		Command        string `json:"command,omitempty"`
		Model          string `json:"model,omitempty"`
		TokensIn       int    `json:"tokens_in,omitempty"`
		TokensOut      int    `json:"tokens_out,omitempty"`
		TokensTotal    int    `json:"tokens_total,omitempty"`
		DurationMs     int64  `json:"duration_ms,omitempty"`
		UsageAvailable bool   `json:"usage_available"` // unconditional — tri-state (sty_56aae77a)
		ArtifactName   string `json:"artifact_name,omitempty"`
		ArtifactType   string `json:"artifact_type,omitempty"`
	}{From: from, To: to, Agent: res.Agent, Skill: res.Skill, Command: res.Command, Model: res.Model,
		TokensIn: res.TokensIn, TokensOut: res.TokensOut, TokensTotal: res.TokensTotal, DurationMs: res.DurationMs,
		UsageAvailable: res.UsageAvailable,
		ArtifactName:   res.ArtifactName, ArtifactType: res.ArtifactType}
	b, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	return b
}

// formatReasoningSuffix appends " reasoning=<text>" when non-empty so accept and
// reject session output share one shape (design §6.2).
func formatReasoningSuffix(reasoning string) string {
	if strings.TrimSpace(reasoning) == "" {
		return ""
	}
	return " reasoning=" + reasoning
}

// multiRejectError builds the transition refuse message for one or more
// rejecting reviewers (sty_4f0a15db). Single-reject keeps the historical format
// so serial short-circuit prose and tests stay stable; multi-reject joins each
// rejecter with notes (and optional reasoning).
func multiRejectError(from, to string, rejects []ReviewerVerdict) error {
	if len(rejects) == 1 {
		rv := rejects[0]
		return fmt.Errorf("transition %s→%s rejected by %s: decision=reject notes=%s%s",
			from, to, rv.Skill, rv.Notes, formatReasoningSuffix(rv.Reasoning))
	}
	var parts []string
	for _, rv := range rejects {
		parts = append(parts, fmt.Sprintf("rejected by %s: notes=%s%s",
			rv.Skill, rv.Notes, formatReasoningSuffix(rv.Reasoning)))
	}
	return fmt.Errorf("transition %s→%s %s", from, to, strings.Join(parts, "; "))
}

// invocationPayload stamps an agent_invocation row with the resolved command and
// the injected-context source (skill/rubric file), correlated to the edge, so the
// timeline can show HOW the agent was invoked alongside its verdict (sty_fb3e0873).
func invocationPayload(from, to string, rv ReviewerVerdict) json.RawMessage {
	p := struct {
		From           string `json:"from"`
		To             string `json:"to"`
		Agent          string `json:"agent"`
		Skill          string `json:"skill,omitempty"`
		Command        string `json:"command,omitempty"`
		Context        string `json:"context,omitempty"`
		Model          string `json:"model,omitempty"`
		TokensIn       int    `json:"tokens_in,omitempty"`
		TokensOut      int    `json:"tokens_out,omitempty"`
		TokensTotal    int    `json:"tokens_total,omitempty"`
		DurationMs     int64  `json:"duration_ms,omitempty"`
		UsageAvailable bool   `json:"usage_available"` // unconditional — tri-state (sty_56aae77a)
	}{From: from, To: to, Agent: "reviewer", Skill: rv.Skill, Command: rv.Command, Context: rv.Context, Model: rv.Model,
		TokensIn: rv.TokensIn, TokensOut: rv.TokensOut, TokensTotal: rv.TokensTotal, DurationMs: rv.DurationMs,
		UsageAvailable: rv.UsageAvailable}
	b, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	return b
}

// summariserInvocationPayload stamps the step summariser's OWN agent_invocation
// row — the same shape invocationPayload uses for a reviewer verdict, so the
// summariser's token/wall-time cost rolls into `satelle story cost` identically
// (closing the documented gap, sty_a699ad14 / sty_b73c3236).
func summariserInvocationPayload(from, to string, result SummaryResult) json.RawMessage {
	p := struct {
		From           string `json:"from"`
		To             string `json:"to"`
		Agent          string `json:"agent"`
		Skill          string `json:"skill,omitempty"`
		Command        string `json:"command,omitempty"`
		Context        string `json:"context,omitempty"`
		Model          string `json:"model,omitempty"`
		TokensIn       int    `json:"tokens_in,omitempty"`
		TokensOut      int    `json:"tokens_out,omitempty"`
		TokensTotal    int    `json:"tokens_total,omitempty"`
		DurationMs     int64  `json:"duration_ms,omitempty"`
		UsageAvailable bool   `json:"usage_available"` // unconditional — tri-state (sty_56aae77a)
	}{From: from, To: to, Agent: "reviewer", Skill: result.Context, Command: result.Command, Context: result.Context, Model: result.Model,
		TokensIn: result.TokensIn, TokensOut: result.TokensOut, TokensTotal: result.TokensTotal, DurationMs: result.DurationMs,
		UsageAvailable: result.UsageAvailable}
	b, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	return b
}
