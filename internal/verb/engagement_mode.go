package verb

import (
	"os"
	"os/exec"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/epicset"
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
//   - epic: the item's PARENT id, so sibling children of one epic co-hold the
//     seat. A parentless story falls back to its own id and therefore claims the
//     seat as a singleton (nothing else can match its key).
//
// The epic id is a string key lifted off ParentID; the parent is never read to
// find it. A parent is not engaged unless its route declares a step with
// after_children, which may name a performer; a container idling at a
// waits_on_children step still holds no lease. An epic-parent that does take a
// seat claims it under its OWN id, which is the key its children share, so
// parent and children co-hold from distinct worktrees.
func seatKeyFor(item workitem.Item) string {
	if engagementParallel == config.ParallelEpic && !epicset.IsEpicParent(item) {
		if p := strings.TrimSpace(item.ParentID); p != "" {
			return p
		}
	}
	return item.ID
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
