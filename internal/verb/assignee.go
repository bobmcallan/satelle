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

// SignedIn reports whether the session acts as a signed-in hosted user: the
// assignee resolver yields a PrincipalID. It is deliberately not the actor
// resolver, which returns the git email for a local-only user — an unwired
// resolver, or one with no credential, is a local-only session.
func SignedIn() bool {
	return assigneeResolver != nil && assigneeResolver() != ""
}

// engageGuard runs before an engaging move's wrong-holder check. It lets the
// CLI refuse an engage whose holder cannot yet be named (a session token whose
// user is not resolved), instead of the empty holder passing refuseWrongHolder
// silently. Unwired is no guard.
var engageGuard func(ctx context.Context) error

// SetEngageGuard wires the pre-engage guard. Pass nil to clear (tests).
func SetEngageGuard(f func(ctx context.Context) error) {
	engageGuard = f
}

func runEngageGuard(ctx context.Context) error {
	if engageGuard == nil {
		return nil
	}
	return engageGuard(ctx)
}

// actorResolver returns the satelle user as a ledger actor: the account
// PrincipalID when signed in, the git email when local-only (wired by the CLI,
// which owns the credential store and git). Distinct from assigneeResolver on
// purpose: a holder id must stay a PrincipalID, and offline there is no holder,
// so the git email never reaches refuseWrongHolder.
var actorResolver func() string

// SetActorResolver wires how person-valued ledger rows resolve the user. Pass
// nil to clear (tests).
func SetActorResolver(f func() string) {
	actorResolver = f
}

// resolveActor is the person actor, or "" when unwired / nothing is known.
func resolveActor() string {
	if actorResolver == nil {
		return ""
	}
	return actorResolver()
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
