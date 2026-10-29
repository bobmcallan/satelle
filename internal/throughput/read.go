package throughput

import (
	"context"
	"fmt"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
	"github.com/bobmcallan/satelle/internal/workspace"
)

// ReadRepo returns the stories and epics of the repo at root that finished in w,
// reading it read-only: its ledger through workspace.ReadLedgerReadOnly, its
// stories and routes through workspace.LoadRepoReadOnly. An item is found by its
// status_transition rows inside w, so one created long before the window and
// finished inside it is never dropped by a page bound.
func ReadRepo(ctx context.Context, root string, w Window) ([]Item, error) {
	var ids []string
	chains := map[string][]ledger.Entry{}
	err := workspace.ReadLedgerReadOnly(ctx, root, func(ctx context.Context, ls *ledger.Store) error {
		seen := map[string]bool{}
		if err := ls.ForEachKind(ctx, "", ledger.KindStatusTransition, func(e ledger.Entry) error {
			if e.StoryID != "" && !seen[e.StoryID] && w.Contains(e.CreatedAt) {
				seen[e.StoryID] = true
				ids = append(ids, e.StoryID)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, id := range ids {
			for _, kind := range []string{ledger.KindStoryCreated, ledger.KindStatusTransition} {
				es, err := ls.ListByStory(ctx, id, kind)
				if err != nil {
					return fmt.Errorf("read ledger of %s: %w", id, err)
				}
				chains[id] = append(chains[id], es...)
			}
		}
		return nil
	})
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	rv, err := workspace.LoadRepoReadOnly(ctx, root, ids...)
	if err != nil {
		return nil, err
	}
	var out []Item
	for _, it := range rv.Stories {
		es, ok := chains[it.ID]
		if !ok || it.Kind != workitem.KindStory {
			continue
		}
		// A story no route resolves for is judged on the store's defaults (Cycle).
		spec, _, _, _ := wfgovern.SpecFor(rv.Workflows, it)
		if fi, ok := Finish(it, es, spec); ok && w.Contains(fi.Finished) {
			out = append(out, fi)
		}
	}
	return out, nil
}

// Build reads every root and assembles the report: one section per repo, and a
// total over every repo that could be read. A repo that cannot be read is
// reported with its error and named in the total's Excluded.
func Build(ctx context.Context, roots []string, w Window) Report {
	rep := Report{Window: w, Repos: []RepoReport{}}
	var all []Item
	var excluded []string
	for _, root := range roots {
		name := config.RepoName(root)
		items, err := ReadRepo(ctx, root, w)
		if err != nil {
			r := Unavailable(name, root, "repo unreadable: "+err.Error(), w)
			r.Error = err.Error()
			rep.Repos = append(rep.Repos, r)
			excluded = append(excluded, name)
			continue
		}
		all = append(all, items...)
		rep.Repos = append(rep.Repos, Summarise(name, root, items, w))
	}
	if len(excluded) == len(roots) {
		rep.Total = Unavailable("total", "", ReasonNoRepo, w)
	} else {
		rep.Total = Summarise("total", "", all, w)
	}
	rep.Total.Excluded = excluded
	return rep
}
