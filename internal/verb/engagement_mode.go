package verb

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Seat concurrency wiring (sty_c098dc2d): the repo's [engagement] parallel mode
// reaches seat arbitration through a package global, the same shape as
// SetTagVocabulary / SetAgentsConfig. The ZERO VALUE is config.ParallelNone, so
// every caller that never wires a mode — tests, pre-init, an ungoverned repo —
// gets today's single performing story by construction.
//
// The mode chooses a KEY POLICY; it is not itself an arbitration rule. Nothing
// below this file (the lease package) learns the mode's name.

var engagementParallel = config.ParallelNone

// SetEngagementMode wires the repo's seat concurrency mode. Called with the
// process config at CLI bootstrap.
func SetEngagementMode(cfg config.Config) {
	engagementParallel = cfg.ResolveEngagementParallel()
}

// ClearEngagementMode restores the default mode (tests).
func ClearEngagementMode() { engagementParallel = config.ParallelNone }

// seatKeyFor returns the arbitration key an item claims the story seat under.
//
//   - none (default): the item's OWN id. Every key is unique, so any other live
//     seat holder conflicts — today's behaviour, expressed as a key rather than
//     as a special case.
//   - epic: the item's EPIC (seatEpicOf), so sibling children of one epic co-hold
//     the seat. A story in no epic falls back to its own id and therefore claims
//     the seat as a singleton (nothing else can match its key). A child of a
//     sequential epic (sequential = true on its container's waits_on_children
//     step) falls back the same way: the route schedule only narrows the mode,
//     it never widens it, so siblings conflict and run one at a time.
//   - worktree: the same epic key (seatPolicyFor adds the flags that stop it
//     excluding unrelated stories).
//
// The container's route is read only for the sequential check, and a failed
// lookup keeps the epic key. A parent is not engaged unless its route declares a
// step with after_children, which may name a performer; a container idling at a
// waits_on_children step still holds no lease. An epic-parent that does take a
// seat claims it under its OWN id, which is the key its children share, so
// parent and children co-hold from distinct worktrees.
func seatKeyFor(ctx context.Context, item workitem.Item) string {
	return seatPolicyFor(ctx, item).Key
}

// seatPolicy is what a claim hands the lease: its arbitration key and the two
// co-hold flags lease.CoHolds reads. It is the lease package's SeatClaim.
type seatPolicy = lease.SeatClaim

// seatPolicyFor is the mode's whole contribution to seat arbitration
// (sty_69ec7fbc): the key, plus — under worktree — the flags that make the
// working tree the arbitration instead of the key.
//
//   - none: the item's own id, a singleton.
//   - epic: the item's epic (seatEpicOf), as seatKeyFor documents.
//   - worktree: the same epic key, TreeScoped, so the claim co-holds with every
//     other tree-scoped holder whatever its epic. The children of a sequential
//     epic are also Exclusive, which makes a holder under their own key refuse
//     them while unrelated stories still co-hold: one child at a time, the same
//     promise a sequential epic makes under epic.
func seatPolicyFor(ctx context.Context, item workitem.Item) seatPolicy {
	own := seatPolicy{Key: item.ID}
	if engagementParallel != config.ParallelEpic && engagementParallel != config.ParallelWorktree {
		return own
	}
	sequential := EpicChildSchedule(ctx, item) == wfdot.SchedSequential
	key := seatEpicOf(ctx, item)
	if key == "" {
		key = item.ID
	}
	if engagementParallel == config.ParallelEpic {
		if sequential {
			return own
		}
		return seatPolicy{Key: key}
	}
	return seatPolicy{Key: key, TreeScoped: true, Exclusive: sequential && key != item.ID}
}

// seatEpicOf is the epic key item claims the seat under: the container the wave
// and the readiness round would place it under — its parent_id epic, or the
// container of an epic:<theme> it carries — so a child that belongs to an epic
// by tag alone shares the key its siblings have (sty_9e3520c2). It is the item's
// own id for an epic-parent, "" when item belongs to no epic or to two (the
// caller then claims as a singleton, failing closed the way EpicChildSchedule
// does), and the plain parent_id when the parent is not an epic-parent, which is
// the rule before membership was consulted.
func seatEpicOf(ctx context.Context, item workitem.Item) string {
	if epicset.IsEpicParent(item) {
		return item.ID
	}
	parent := strings.TrimSpace(item.ParentID)
	store, err := requireWorkItem()
	if err != nil {
		return parent
	}
	switch containers := waveContainersOf(ctx, store, item); len(containers) {
	case 0:
		return parent
	case 1:
		return containers[0].ID
	}
	return ""
}

// engagementWorktree resolves the git working tree this process is engaging
// from — the anchor recorded on the lease and on the engagement baseline.
// Empty when git cannot answer (non-repo caller, no git binary): an empty
// anchor opts out of tree arbitration rather than blocking on a guess.
func engagementWorktree() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return gitToplevel(dir)
}

// gitToplevel resolves dir's git working-tree root. Empty when dir is not in a
// working tree. ONE resolver so the lease anchor and `story diff` cannot drift
// into comparing differently-derived paths.
func gitToplevel(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
