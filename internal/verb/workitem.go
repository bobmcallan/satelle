package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
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
		}, now)
		if err != nil {
			return nil, err
		}
		appendLedger(ctx, it.ID, ledgerKind, fmt.Sprintf("created %s %q", kind, it.Title), now)
		if stampedWorkflow != "" {
			appendLedger(ctx, it.ID, ledger.KindWorkflowStamped,
				fmt.Sprintf("governing workflow: %s", stampedWorkflow), now)
		}
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
	transitioning := req.Status != nil && *req.Status != current.Status

	// Gate the transition through the isolated reviewer, if one is wired and the
	// edge is governed by a reviewer skill. A reject blocks the whole set and
	// pushes the reviewer's notes back to the executor; an ungated edge enacts.
	gatedAccepted := false
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
			reviewers = []ReviewerVerdict{{Skill: dec.Skill, Accept: dec.Accept, Notes: dec.Notes, Command: dec.Command, Context: dec.Context}}
		}
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
				notifyChange(panelTopic(current.Kind))
				return nil, fmt.Errorf("transition %s→%s rejected by %s: %s",
					current.Status, *req.Status, rv.Skill, rv.Notes)
			}
			gatedAccepted = true
			appendLedgerEntry(ctx, current.ID, ledger.KindReviewAccept, "reviewer",
				fmt.Sprintf("accepted %s→%s by %s", current.Status, *req.Status, rv.Skill),
				reviewerPayload(current.Status, *req.Status, rv), now)
		}
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
	}, now)
	if err != nil {
		return nil, err
	}
	if transitioning {
		// An enacted status change records a transition row (feeds the progress
		// column), regardless of whether the edge was gated.
		appendLedgerEntry(ctx, it.ID, ledger.KindStatusTransition, "executor",
			fmt.Sprintf("%s → %s", current.Status, *req.Status),
			transitionPayload(current.Status, *req.Status, ""), now)
		// After a GATED transition is enacted, the read-only summariser recaps the
		// step into a step_summary row — but ONLY where the active workflow declares
		// a step-summary node (transparent opt-in; sty_9a139c78). The transition
		// already committed, so a mandatory-summary failure is surfaced on the
		// ledger (it records the gap, it does not revert the step).
		if gatedAccepted && stepSummariser != nil {
			summary, serr := stepSummariser.Summarise(ctx, it, current.Status, *req.Status)
			switch {
			case serr != nil:
				appendLedgerEntry(ctx, it.ID, ledger.KindStepSummary, "reviewer",
					"step summary failed: "+serr.Error(),
					transitionPayload(current.Status, *req.Status, ""), now)
			case summary != "":
				appendLedgerEntry(ctx, it.ID, ledger.KindStepSummary, "reviewer", summary,
					transitionPayload(current.Status, *req.Status, ""), now)
			}
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
		From   string `json:"from"`
		To     string `json:"to"`
		Skill  string `json:"skill,omitempty"`
		Order  int    `json:"order"`
		System bool   `json:"system,omitempty"`
	}{From: from, To: to, Skill: rv.Skill, Order: rv.Order, System: rv.System}
	b, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	return b
}

// invocationPayload stamps an agent_invocation row with the resolved command and
// the injected-context source (skill/rubric file), correlated to the edge, so the
// timeline can show HOW the agent was invoked alongside its verdict (sty_fb3e0873).
func invocationPayload(from, to string, rv ReviewerVerdict) json.RawMessage {
	p := struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Agent   string `json:"agent"`
		Skill   string `json:"skill,omitempty"`
		Command string `json:"command,omitempty"`
		Context string `json:"context,omitempty"`
	}{From: from, To: to, Agent: "reviewer", Skill: rv.Skill, Command: rv.Command, Context: rv.Context}
	b, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	return b
}
