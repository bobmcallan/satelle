package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Story and task are the same primitive (workitem) distinguished by kind, so
// the create/list handlers are produced per-kind by a factory and the
// kind-agnostic get/set handlers are shared. Each is registered under both the
// "story-" and "task-" name prefixes so the CLI groups map 1:1.
func init() {
	for _, kind := range []workitem.Kind{workitem.KindStory, workitem.KindTask} {
		group := string(kind)
		Register(&Verb{Name: group + "-create", Description: "Create a " + group, Invoke: workItemCreate(kind)})
		Register(&Verb{Name: group + "-list", Description: "List " + group + "s", Invoke: workItemList(kind)})
		Register(&Verb{Name: group + "-get", Description: "Get a " + group + " by id", Invoke: workItemGet})
		Register(&Verb{Name: group + "-set", Description: "Update a " + group, Invoke: workItemSet})
	}
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

func workItemCreate(kind workitem.Kind) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	ledgerKind := ledger.KindStoryCreated
	if kind == workitem.KindTask {
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
		// judges the draft before it is persisted. A reject blocks creation and
		// pushes the notes back to the executor; ungated/accept persists.
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

		it, err := store.Create(ctx, workitem.CreateInput{
			Kind:               kind,
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
		appendLedger(ctx, it.ID, ledgerKind, fmt.Sprintf("created %s %q", kind, it.Title), now)
		writeStoryFile(it)
		notifyChange(panelTopic(kind))
		return json.Marshal(it)
	}
}

// panelTopic maps a work-item kind to its realtime panel topic.
func panelTopic(kind workitem.Kind) string {
	if kind == workitem.KindTask {
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
			reviewers = []ReviewerVerdict{{Skill: dec.Skill, Accept: dec.Accept, Notes: dec.Notes}}
		}
		for _, rv := range reviewers {
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
		// step into a step_summary row. Best-effort: the work already committed, so
		// a summariser failure must not fail the transition.
		if gatedAccepted && stepSummariser != nil {
			if summary, serr := stepSummariser.Summarise(ctx, it, current.Status, *req.Status); serr == nil && summary != "" {
				appendLedgerEntry(ctx, it.ID, ledger.KindStepSummary, "reviewer", summary,
					transitionPayload(current.Status, *req.Status, ""), now)
			}
		}
	} else {
		ledgerKind := ledger.KindStoryUpdated
		if it.Kind == workitem.KindTask {
			ledgerKind = ledger.KindTaskUpdated
		}
		appendLedger(ctx, it.ID, ledgerKind, fmt.Sprintf("updated %s", it.Kind), now)
	}
	writeStoryFile(it)
	notifyChange(panelTopic(it.Kind))
	return json.Marshal(it)
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
