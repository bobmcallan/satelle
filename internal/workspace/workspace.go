// Package workspace aggregates several satelle repos into one read-only view.
// Each registered repo's per-repo database stays the source of truth; this opens
// each in turn and reads its stories/tasks/docs through the domain stores, so the
// workspace is an aggregation layer, not a second database. A repo that fails to
// open is reported (with its error) rather than failing the whole aggregate.
package workspace

import (
	"context"
	"path/filepath"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// RepoView is one repo's slice of the aggregate.
type RepoView struct {
	Path    string
	Name    string // base name of the repo dir, for display
	Stories []workitem.Item
	Tasks   []workitem.Item
	Docs    []docindex.Doc
	Err     string // non-empty when this repo could not be read
}

// Aggregate is the merged view across the registered repos.
type Aggregate struct {
	Repos []RepoView
}

// Totals returns the summed story/task/doc counts across readable repos.
func (a Aggregate) Totals() (stories, tasks, docs int) {
	for _, r := range a.Repos {
		stories += len(r.Stories)
		tasks += len(r.Tasks)
		docs += len(r.Docs)
	}
	return
}

// Load aggregates the given repo roots. Order is preserved; duplicates are kept
// as given (the caller de-dups). Each repo is opened, read, and closed.
func Load(ctx context.Context, repoRoots []string) Aggregate {
	var agg Aggregate
	for _, root := range repoRoots {
		agg.Repos = append(agg.Repos, loadRepo(ctx, root))
	}
	return agg
}

func loadRepo(ctx context.Context, root string) RepoView {
	rv := RepoView{Path: root, Name: filepath.Base(root)}
	dbPath := filepath.Join(root, config.DefaultDataDir, config.DefaultDBName)
	db, err := store.Open(dbPath)
	if err != nil {
		rv.Err = err.Error()
		return rv
	}
	defer db.Close()

	if stories, err := db.Stories.List(ctx, workitem.ListFilter{Kind: workitem.KindStory}); err == nil {
		rv.Stories = stories
	} else {
		rv.Err = err.Error()
	}
	if tasks, err := db.Stories.List(ctx, workitem.ListFilter{Kind: workitem.KindTask}); err == nil {
		rv.Tasks = tasks
	}
	if docs, err := db.DocIndex.List(ctx, ""); err == nil {
		rv.Docs = docs
	}
	return rv
}
