package cli

// Story-scoped work-state sync (sty_991b6900): `sync workstate push|pull|snapshot
// <id>...` moves only the named stories — an epic container stands for itself and
// every story carrying its epic:<theme> tag — and the ledger rows whose story_id
// is among them. This file owns what "in scope" means and how ids resolve; the
// push filter and the scoped pull read it, so they cannot disagree.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// maxScopeIDs is the most ids satelle-server accepts in SnapshotRequest.story_ids.
const maxScopeIDs = 1000

// scopeSet is the resolved story-id set of a scoped sync. A nil set is the
// unscoped sync and admits everything.
type scopeSet map[string]bool

// HasItem reports whether a work item (by its own id) is in scope.
func (s scopeSet) HasItem(id string) bool { return s == nil || s[id] }

// HasLedger reports whether a ledger row (by its story_id) is in scope.
func (s scopeSet) HasLedger(storyID string) bool { return s == nil || s[storyID] }

// scopeResolution is what the ids on the command line resolved to.
type scopeResolution struct {
	// IDs is the deduped, sorted wire set (SnapshotRequest.story_ids); empty when
	// the sync is unscoped.
	IDs []string
	Set scopeSet
	// Missing holds the named ids the local store does not have (pull only; a
	// push refuses them).
	Missing map[string]bool
	// EmptyEpics holds epic containers held locally that have no members locally.
	EmptyEpics []string
}

func (r scopeResolution) scoped() bool { return len(r.IDs) > 0 }

// resolveScope turns the command-line ids into a scope. An id in the local store
// that is an epic container stands for the container plus its members (epicset,
// the same resolution `story wave` uses); any other id stands for itself. An id
// the store lacks is refused on push — there is nothing to send — and on pull is
// a plain id the server is asked for.
func resolveScope(ctx context.Context, a *app.App, ids []string, push bool) (scopeResolution, error) {
	var res scopeResolution
	if len(ids) == 0 {
		return res, nil
	}
	set := scopeSet{}
	res.Missing = map[string]bool{}
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			return res, errors.New("empty story id")
		}
		it, err := a.Store.Stories.Get(ctx, id)
		switch {
		case errors.Is(err, workitem.ErrNotFound):
			if push {
				return res, fmt.Errorf("unknown story or epic id %q in this store", id)
			}
			set[id] = true
			res.Missing[id] = true
			continue
		case err != nil:
			return res, fmt.Errorf("look up %s: %w", id, err)
		}
		set[id] = true
		if !epicset.IsEpicParent(it) {
			continue
		}
		members, err := epicset.Members(ctx, a.Store.Stories, it)
		if err != nil {
			return res, fmt.Errorf("resolve epic %s: %w", id, err)
		}
		if len(members) == 0 {
			res.EmptyEpics = append(res.EmptyEpics, id)
		}
		for _, m := range members {
			set[m.ID] = true
		}
	}
	if len(set) > maxScopeIDs {
		return res, fmt.Errorf("scope resolves to %d ids; the server accepts at most %d", len(set), maxScopeIDs)
	}
	for id := range set {
		res.IDs = append(res.IDs, id)
	}
	slices.Sort(res.IDs)
	res.Set = set
	slices.Sort(res.EmptyEpics)
	return res, nil
}

// emptyEpicNotice says a synced epic container arrived without its members. The
// server does not expand an epic and membership is read from the local store, so
// naming the container again cannot bring the members in — naming them can.
func emptyEpicNotice(w io.Writer, id string) {
	fmt.Fprintf(w, "epic %s: container only; its members were not synced because none are in the local store — name the member story ids (satelle sync workstate pull <member-id>...) to sync them\n", id)
}

// errScopeIgnored marks a Snapshot response carrying rows outside the requested
// ids: the server does not apply story_ids (an older satelle-server drops the
// unknown field and answers with the whole project).
var errScopeIgnored = errors.New("does not support story-scoped Snapshot")

// runScopedWorkstateSnapshot pulls the resolved ids in pages and applies them
// through the same apply and reconcile path as the full pass. It differs in what
// it refuses and what it leaves alone: every page is held and checked first, so
// a server that ignores the scope, a missing id or a rejected page token leaves
// the store untouched, and it never falls back to an unscoped pass. It reads,
// writes and clears no since-token or checkpoint — those belong to the project
// pass, and a scoped pass is always complete for its ids. Holding the pages is
// bounded by the 1000-id scope cap.
func runScopedWorkstateSnapshot(ctx context.Context, a *app.App, errw io.Writer, client *hosted.Client, server, project string, optIn map[string]bool, force bool, mode snapshotMode, sc scopeResolution) (passReport, error) {
	p := &snapshotPass{
		a: a, errw: errw, server: server, project: project,
		optIn: optIn, force: force, mode: mode,
		rep: passReport{Kind: passScoped}, pending: map[string]string{},
	}
	var pages []hosted.SnapshotPage
	got := map[string]hosted.WorkstateItem{}
	onPage := func(pg hosted.SnapshotPage) error {
		for _, it := range pg.Items {
			if !sc.Set.HasItem(it.ID) {
				return fmt.Errorf("%w — returned work item %s outside the requested ids", errScopeIgnored, it.ID)
			}
			got[it.ID] = it
		}
		for _, row := range pg.Ledger {
			if !sc.Set.HasLedger(row.StoryID) {
				return fmt.Errorf("%w — returned ledger row %s (story %s) outside the requested ids", errScopeIgnored, row.ID, row.StoryID)
			}
		}
		pages = append(pages, pg)
		return nil
	}
	res, err := client.Snapshot(ctx, project, "", hosted.SnapshotOptions{StoryIDs: sc.IDs}, onPage)
	p.rep.Pages = res.Pages
	switch {
	case errors.Is(err, errScopeIgnored):
		return p.rep, fmt.Errorf("server %s %w; nothing applied", server, err)
	case errors.Is(err, hosted.ErrSinceTokenRejected):
		return p.rep, fmt.Errorf("scoped snapshot page token rejected by %s (%s); nothing applied — re-run the scoped pull",
			server, strings.TrimPrefix(err.Error(), hosted.ErrSinceTokenRejected.Error()+": "))
	case err != nil:
		return p.rep, fmt.Errorf("scoped snapshot from %s: %w; nothing applied", server, err)
	}
	var absent []string
	for _, id := range sc.IDs {
		if _, ok := got[id]; !ok {
			absent = append(absent, id)
		}
	}
	if len(absent) > 0 {
		return p.rep, fmt.Errorf("story id(s) %s not found on %s; nothing applied", strings.Join(absent, ", "), server)
	}

	if mode == snapshotModePull && !force {
		for _, pg := range pages {
			items, ledgerRows := p.optedIn(pg)
			if err := checkWorkstatePullConflicts(ctx, a, optIn, items, ledgerRows); err != nil {
				return p.rep, err
			}
		}
	}
	for _, pg := range pages {
		if err := p.apply(ctx, pg); err != nil {
			if p.rep.Items+p.rep.Ledger > 0 {
				_, _, _ = verb.SyncStoryBacklog(ctx, a.Store.Stories, time.Now().UTC())
			}
			return p.rep, err
		}
	}
	if err := p.settle(ctx, time.Now().UTC()); err != nil {
		return p.rep, err
	}

	// A container the local store did not hold arrived on its own: the server does
	// not expand an epic. Say so rather than leave a silent half-sync.
	notice := slices.Clone(sc.EmptyEpics)
	for id := range sc.Missing {
		it, perr := parseWorkstateItem(got[id])
		if perr != nil || !epicset.IsEpicParent(it) {
			continue
		}
		if members, merr := epicset.Members(ctx, a.Store.Stories, it); merr != nil || len(members) == 0 {
			notice = append(notice, id)
		}
	}
	slices.Sort(notice)
	for _, id := range notice {
		emptyEpicNotice(errw, id)
	}
	return p.rep, nil
}
