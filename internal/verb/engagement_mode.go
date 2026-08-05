package verb

import (
	"os"
	"os/exec"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
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

// SetEngagementMode wires the repo's seat concurrency mode. Called with
// a.Config at CLI bootstrap.
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
// The parent is NEVER engaged and is never even read: the epic id is a string
// key lifted off ParentID. Do not "fix" this by leasing or loading the parent —
// container stories keep their judge-only routes on purpose.
func seatKeyFor(item workitem.Item) string {
	if engagementParallel == config.ParallelEpic {
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
