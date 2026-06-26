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

	"github.com/google/uuid"
)

// Kind discriminators for the canonical entry shapes the work layer emits.
// Callers may append entries of any kind; these are the names satelle uses.
const (
	KindStoryCreated = "story_created"
	KindStoryUpdated = "story_updated"
	KindTaskCreated  = "task_created"
	KindTaskUpdated  = "task_updated"
	KindComment      = "comment"
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

// NewID returns a fresh ledger-entry id in the evt_<8hex> form, visually
// distinct from sty_/tsk_ ids in tool output.
func NewID() string { return fmt.Sprintf("evt_%s", uuid.NewString()[:8]) }

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
