// Package workitem is satelle's unit-of-work dynamic primitive — stories and
// tasks. Both share one shape and one backing table, distinguished by Kind,
// mirroring satellites' unification of story/task behind a single documents
// row (type='story'|'task'). A story is a goal; a task is a project-level
// to-do; they differ only in id prefix and ledger-event kind.
//
// Backed by sqlite (modernc.org/sqlite, no cgo); SQL is libSQL-compatible.
package workitem

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Kind discriminates stories from tasks. It selects the id prefix and the
// table partition (a `kind` column), so one Store serves both.
type Kind string

const (
	KindStory Kind = "story"
	KindTask  Kind = "task"
	// KindExecution is an isolated RUN of a task (sty_ef08ce2a). A task is a
	// stable authored header/work-definition; each execution is a separate item
	// parented to its task, carrying the run lifecycle backlog→in_progress→done.
	// "Re-running" a task means creating a NEW execution — a done execution is
	// terminal and never moved backward (satelle-done-is-last).
	KindExecution Kind = "execution"
)

// Status values. New items default to StatusBacklog — every satelle workflow
// starts at backlog (see satelle-workflow-review). The set is open-ended (a
// caller may set any status string); these are the names satelle itself uses.
const (
	StatusBacklog    = "backlog"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
	StatusBlocked    = "blocked"
)

// Item is one story or task row.
type Item struct {
	ID                 string    `json:"id"`
	Kind               Kind      `json:"kind"`
	Title              string    `json:"title"`
	Body               string    `json:"body,omitempty"`
	Status             string    `json:"status"`
	Priority           string    `json:"priority,omitempty"`
	Category           string    `json:"category,omitempty"`
	ParentID           string    `json:"parent_id,omitempty"`
	AcceptanceCriteria string    `json:"acceptance_criteria,omitempty"`
	Tags               []string  `json:"tags"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// idPrefix returns the id prefix for a kind: sty_ for stories, tsk_ for tasks,
// exe_ for executions.
func (k Kind) idPrefix() string {
	switch k {
	case KindTask:
		return "tsk_"
	case KindExecution:
		return "exe_"
	default:
		return "sty_"
	}
}

// newID returns a fresh id for the kind in the <prefix><8hex> form.
func (k Kind) newID() string {
	return fmt.Sprintf("%s%s", k.idPrefix(), uuid.NewString()[:8])
}

// valid reports whether k is a known kind.
func (k Kind) valid() bool {
	return k == KindStory || k == KindTask || k == KindExecution
}
