package verb

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Concurrency is configuration (sty_9c782f0e): [engagement] limit bounds how
// many stories may perform at once, and an epic whose waits_on_children step
// sets sequential = true admits one performing child at a time. Both are
// counted from the stories whose chain folds to a performing state; nothing is
// held. The ZERO VALUE is unlimited, so a caller that never wires a limit —
// tests, pre-init, an ungoverned repo — admits every story.

var engagementLimit int

// SetEngagementMode wires the repo's concurrency limit. Called with the process
// config at CLI bootstrap.
func SetEngagementMode(cfg config.Config) {
	engagementLimit = cfg.ResolveEngagementLimit()
}

// ClearEngagementMode restores the default, unlimited (tests).
func ClearEngagementMode() { engagementLimit = 0 }

// PerformingStories lists the stories whose chain folds to a performing state
// of their governing workflow, oldest first. A story whose row is terminal is
// not folded: a terminal story cannot reopen (refuseReopen), so its chain cannot
// fold to a performing state.
func PerformingStories(ctx context.Context) ([]workitem.Item, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	rows, err := store.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Limit: 2000})
	if err != nil {
		return nil, fmt.Errorf("satelle: cannot count performing stories (list failed: %w)", err)
	}
	var out []workitem.Item
	for _, row := range rows {
		if targetIsTerminalStateOnly(ctx, row, row.Status) {
			continue
		}
		it, ferr := foldedItem(ctx, row)
		if ferr != nil {
			it = row
		}
		if engaging, ok := storyStatusIsEngaging(ctx, it, it.Status); ok && engaging && !waitsOnOpenChildrenAt(ctx, it, it.Status) {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// checkConcurrency refuses engaging item at targetStatus when that would break
// the configured concurrency: a sequential epic item belongs to already has a
// performing child, or the project limit is already reached. Only stories are
// counted and only stories are refused; a task never contends. Callers have
// already established that targetStatus is a performing state.
func checkConcurrency(ctx context.Context, item workitem.Item, targetStatus string) error {
	if item.Kind != workitem.KindStory {
		return nil
	}
	self := item.ID
	if self == "" {
		self = "new story"
	}
	sequential := sequentialContainersOf(ctx, item)
	if len(sequential) == 0 && engagementLimit <= 0 {
		return nil
	}
	performing, err := PerformingStories(ctx)
	if err != nil {
		return err
	}
	var others []workitem.Item
	for _, p := range performing {
		if p.ID != item.ID {
			others = append(others, p)
		}
	}
	if len(sequential) > 0 {
		for _, other := range others {
			for c := range sequentialContainersOf(ctx, other) {
				if sequential[c] {
					return fmt.Errorf(
						"satelle: refusing to engage %s (→ %s): epic %s is sequential and %s is performing (status %q) — finish or park it (blocked) first",
						self, targetStatus, c, describeStory(other), other.Status)
				}
			}
		}
	}
	if engagementLimit > 0 && len(others) >= engagementLimit {
		names := make([]string, 0, len(others))
		for _, o := range others {
			names = append(names, fmt.Sprintf("%s (status %q)", describeStory(o), o.Status))
		}
		return fmt.Errorf(
			"satelle: refusing to engage %s (→ %s): [engagement] limit = %d and %d already performing — %s; finish or park one (blocked) first. Never cancel a healthy story to get under the limit — cancelled is terminal",
			self, targetStatus, engagementLimit, len(others), strings.Join(names, "; "))
	}
	return nil
}

// sequentialContainersOf is the set of epic containers item is a child of whose
// route declares sequential = true.
func sequentialContainersOf(ctx context.Context, item workitem.Item) map[string]bool {
	store, err := requireWorkItem()
	if err != nil {
		return nil
	}
	idx, err := requireDocIndex()
	if err != nil {
		return nil
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, c := range waveContainersOf(ctx, store, item) {
		if ContainerSchedule(wfs, c) == wfdot.SchedSequential {
			out[c.ID] = true
		}
	}
	return out
}

// describeStory names a story with its title for a refusal.
func describeStory(it workitem.Item) string {
	if it.Title == "" {
		return it.ID
	}
	return it.ID + ` "` + it.Title + `"`
}

// engagementWorktree resolves the git working tree this process is engaging
// from — the tree recorded on the working_on row and on the engagement
// baseline. Empty when git cannot answer (non-repo caller, no git binary).
func engagementWorktree() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return gitToplevel(dir)
}

// gitToplevel resolves dir's git working-tree root. Empty when dir is not in a
// working tree. ONE resolver so the working_on tree and `story diff` cannot
// drift into comparing differently-derived paths.
func gitToplevel(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
