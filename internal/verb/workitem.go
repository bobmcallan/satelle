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
	ledgerKind := ledger.KindStoryUpdated
	if it.Kind == workitem.KindTask {
		ledgerKind = ledger.KindTaskUpdated
	}
	appendLedger(ctx, it.ID, ledgerKind, fmt.Sprintf("updated %s", it.Kind), now)
	notifyChange(panelTopic(it.Kind))
	return json.Marshal(it)
}

// appendLedger records a work-item lifecycle event, best-effort: a ledger
// failure must not fail the work mutation that already committed. Skipped when
// no ledger store is wired.
func appendLedger(ctx context.Context, storyID, kind, body string, now time.Time) {
	if ledgerStore == nil {
		return
	}
	_, _ = ledgerStore.Append(ctx, ledger.AppendInput{
		StoryID: storyID,
		Kind:    kind,
		Body:    body,
	}, now)
}
