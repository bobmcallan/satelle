package verb

import (
	"context"
	"fmt"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// assigneeResolver returns the current holder's hosted principal id from a
// local file read (wired by the CLI from hosted.FileStore). Empty means no
// credential / no PrincipalID — create and engage leave assignee empty.
// verb must not import hosted: this seam keeps OAuth out of the story path.
var assigneeResolver func() string

// SetAssigneeResolver wires how create/engage resolve the current holder.
// Pass nil to clear (tests).
func SetAssigneeResolver(f func() string) {
	assigneeResolver = f
}

// ClearAssigneeResolver unsets the resolver (tests). Unwired is the no-
// credential path: empty assignee, engage still allowed.
func ClearAssigneeResolver() {
	assigneeResolver = nil
}

func resolveAssignee() string {
	if assigneeResolver == nil {
		return ""
	}
	return assigneeResolver()
}

// refuseWrongHolder refuses an engaging move when the item is assigned to a
// different principal. Empty me or empty assignee is allowed (offline / pre-
// column). The message names the holder; it does not say allocated or actor.
func refuseWrongHolder(current workitem.Item, me string) error {
	if me == "" || current.Assignee == "" || current.Assignee == me {
		return nil
	}
	return fmt.Errorf("%s is assigned to %s — only its assignee may engage it", current.ID, current.Assignee)
}

// stampEmptyAssignee returns the current principal to persist when an
// engaging move first-holds an unassigned row. Nil means leave the column.
func stampEmptyAssignee(ctx context.Context, current workitem.Item, target *string) *string {
	if target == nil {
		return nil
	}
	if eng, ok := storyStatusIsEngaging(ctx, current, *target); !ok || !eng {
		return nil
	}
	me := resolveAssignee()
	if me == "" || current.Assignee != "" {
		return nil
	}
	return &me
}
