// Package workspace aggregates several satelle repos into one read-only view.
// Each registered repo's per-repo database stays the source of truth; this opens
// each in turn and reads its stories/tasks/docs through the domain stores, so the
// workspace is an aggregation layer, not a second database. A repo that fails to
// open is reported (with its error) rather than failing the whole aggregate.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/epicset"
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
	// Workflows is the repo's effective workflow docs; set only by LoadRepoReadOnly.
	Workflows []docindex.Doc
	Err       string // non-empty when this repo could not be read
}

// Members returns the container's member set within this repo — epicset's
// answer over the repo's stories (sty_9f4f8e12): the epic: tag set for an
// epic-parent, parent_id links for any other container. The same set the close
// guard, the close-gate payload, the web story view and the CLI route view
// report; the error says why an epic-parent's set cannot be fixed.
func (r RepoView) Members(containerID string) ([]epicset.Ref, error) {
	for _, s := range r.Stories {
		if s.ID != containerID {
			continue
		}
		members, err := epicset.MembersFromItems(r.Stories, s)
		return epicset.Refs(members), err
	}
	return nil, nil
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

// registered reads the workspace registry's repo paths. A var so a test can stand
// in a registry; production reads the global config.
var registered = func() ([]string, error) {
	gc, err := config.LoadGlobal()
	if err != nil {
		return nil, err
	}
	return gc.Workspace.Repos, nil
}

// FindRegistered returns the canonical roots of the registered repos named name
// (config.RepoName), each once. Zero roots means no registered repo has the name;
// more than one means the name is ambiguous — the caller refuses either rather
// than guessing, because two repos can share a directory name.
func FindRegistered(name string) ([]string, error) {
	repos, err := registered()
	if err != nil {
		return nil, err
	}
	var roots []string
	seen := map[string]bool{}
	for _, r := range repos {
		canon := config.CanonicalRepoRoot(r)
		if seen[canon] || config.RepoName(canon) != name {
			continue
		}
		seen[canon] = true
		roots = append(roots, canon)
	}
	return roots, nil
}

// LoadRepoReadOnly reads one repo's stories and authored workflows without
// writing to it: the database through store.OpenReadOnly (no create, no migrate)
// and the workflows through docindex.ReadWorkflowDocs (no index write). It is how
// one repo's session judges another's work. Every failure is an error naming what
// could not be read — a repo that cannot be read is never an empty one.
//
// With ids, Stories is exactly those stories and every container above them —
// each parent_id ancestor and each epic-parent (epicset.ParentOf), at every
// level — read by id, so no page bound can drop a container. A story or parent
// that does not exist is simply absent; the caller decides what absence means.
// Without ids, Stories is the newest storyPage stories.
func LoadRepoReadOnly(ctx context.Context, root string, ids ...string) (RepoView, error) {
	rv := RepoView{Path: root, Name: config.RepoName(root)}
	if _, ok := config.FindDataDir(root); !ok {
		return rv, fmt.Errorf("%s is not a satelle repo (no .satelle/)", root)
	}
	cfg, _, _ := config.Load(filepath.Join(root, config.DefaultDataDir, config.ConfigName))
	process, processRoot, _, err := config.LoadRepoProcess(root)
	if err != nil {
		return rv, fmt.Errorf("load process: %w", err)
	}
	dir := config.ResolveProcessAuthoredDirs(process, processRoot)["workflows"]
	rv.Workflows = docindex.ReadWorkflowDocs(dir, store.EmbeddedDefaultDocs())
	if path, reason, bad := docindex.UnreadableOf(rv.Workflows); bad {
		return rv, fmt.Errorf("workflows %s unreadable: %s", path, reason)
	}
	dbPath := cfg.ResolveDB(root)
	db, err := store.OpenReadOnly(dbPath)
	if err != nil {
		return rv, err
	}
	defer db.Close()
	if len(ids) > 0 {
		rv.Stories, err = readContainment(ctx, db.Stories, ids)
		return rv, err
	}
	stories, err := db.Stories.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Limit: storyPage})
	if err != nil {
		return rv, fmt.Errorf("read stories: %w", err)
	}
	rv.Stories = stories
	return rv, nil
}

// readContainment reads ids and every container above them by id.
func readContainment(ctx context.Context, st *workitem.Store, ids []string) ([]workitem.Item, error) {
	var out []workitem.Item
	seen := map[string]bool{}
	queue := append([]string(nil), ids...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		it, err := st.Get(ctx, id)
		if errors.Is(err, workitem.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read stories: %s: %w", id, err)
		}
		out = append(out, it)
		queue = append(queue, it.ParentID)
		for _, theme := range epicset.Themes(it) {
			p, found, err := epicset.ParentOf(ctx, st, theme)
			if err != nil {
				return nil, fmt.Errorf("read stories: epic set %s of %s: %w", theme, id, err)
			}
			if found {
				queue = append(queue, p.ID)
			}
		}
	}
	return out, nil
}

// storyPage is the store's maximum page, so a repo's stories (and an epic set read
// from them) are not cut short by the default 500.
const storyPage = 2000

func loadRepo(ctx context.Context, root string) RepoView {
	rv := RepoView{Path: root, Name: config.RepoName(root)}
	// Home-keyed runtime plane (sty_4660bbe1): resolve DB the same way app.Open does.
	cfg, _, _ := config.Load(filepath.Join(root, config.DefaultDataDir, config.ConfigName))
	// Same governance guard as app.Open (sty_20a7824c). This is a READ-ONLY
	// aggregation over registered repos, so a repo still in the registry whose
	// .satelle/ has since been deleted must be reported, not silently given a
	// fresh runtime plane on every `satelle service status`.
	if _, ok := config.FindDataDir(root); !ok {
		rv.Err = "not a satelle repo (no .satelle/) — run `satelle init` there, or clear the registry entry"
		return rv
	}
	dbPath := cfg.ResolveDB(root)
	db, err := store.Open(dbPath)
	if err != nil {
		rv.Err = err.Error()
		return rv
	}
	defer db.Close()

	// The store's maximum page, so Members (an epic set read from these rows)
	// is not cut short by the default 500 — the same bound epicset reads under.
	if stories, err := db.Stories.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Limit: storyPage}); err == nil {
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
