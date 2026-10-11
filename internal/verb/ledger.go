package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
)

func init() {
	Register(&Verb{Name: "ledger-append", Description: "Append an entry to the evidence ledger", Invoke: ledgerAppend})
	Register(&Verb{Name: "ledger-list", Description: "List ledger entries for a story/project/kind", Invoke: ledgerList})
}

// ledgerAppendReq is the request body for ledger-append. Kind is required.
type ledgerAppendReq struct {
	StoryID   string          `json:"story_id,omitempty"`
	ProjectID string          `json:"project_id,omitempty"`
	Kind      string          `json:"kind"`
	Actor     string          `json:"actor,omitempty"`
	Body      string          `json:"body,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Refs      json.RawMessage `json:"refs,omitempty"`
}

// The ledger Actor field is the event-author (who recorded the event), a recorded
// internal exemption from the actor→agent rename (sty_7db2ed7d) — see the note in
// internal/ledger/ledger.go — so it stays the "actor" JSON key.

func ledgerAppend(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireLedger()
	if err != nil {
		return nil, err
	}
	var req ledgerAppendReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.Kind == "" {
		return nil, fmt.Errorf("verb: kind required")
	}
	e, err := store.Append(ctx, ledger.AppendInput{
		StoryID:   req.StoryID,
		ProjectID: req.ProjectID,
		Kind:      req.Kind,
		Actor:     req.Actor,
		Body:      req.Body,
		Payload:   req.Payload,
		Refs:      req.Refs,
	}, time.Now())
	if err != nil {
		return nil, err
	}
	return json.Marshal(e)
}

// ledgerListReq is the request body for ledger-list. At least one of
// story_id/project_id/kind must be set (the store refuses an unfiltered scan).
type ledgerListReq struct {
	StoryID   string `json:"story_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	AfterID   string `json:"after_id,omitempty"`
}

func ledgerList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireLedger()
	if err != nil {
		return nil, err
	}
	var req ledgerListReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	entries, err := store.List(ctx, ledger.ListFilter{
		StoryID:   req.StoryID,
		ProjectID: req.ProjectID,
		Kind:      req.Kind,
		Limit:     req.Limit,
		AfterID:   req.AfterID,
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(entries)
}

// ListStoryLedger is the in-process form of `satelle ledger list --story`.
// It uses the same filter the CLI does (story id only; limit 0 so the store
// default applies) and returns the list JSON. A missing store is
// ErrStoreNotConfigured. Callers must not shell out to the CLI.
func ListStoryLedger(ctx context.Context, storyID string) ([]byte, error) {
	store, err := requireLedger()
	if err != nil {
		return nil, err
	}
	entries, err := store.List(ctx, ledger.ListFilter{StoryID: storyID})
	if err != nil {
		return nil, err
	}
	return json.Marshal(entries)
}
