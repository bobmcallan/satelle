package cli

// Work-state in hosted mode (sty_0dc600e1): with the user signed in and the repo
// bound, the server is the store of record. A push sends the item rows of the
// stories this machine authored and nothing else. In team scope the local ledger
// keeps full history only for the stories verb.HeldStories names, and every other
// story's history is dropped once the server holds all of it (sty_7c01ca14).
// Personal scope keeps every chain; local only, none of this runs.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/syncstate"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// repoNotBound reports, and says on w, that the repo names no hosted project: the
// slug ResolveBoundProject falls back to does not bind a repo, so nothing syncs.
func repoNotBound(w io.Writer, a *app.App, what string) bool {
	if verb.RepoBound(a.Config) {
		return false
	}
	fmt.Fprintf(w, "%s: repo not bound — set [sync] project (\"satelle project bind <slug>\"), or run \"satelle init\" to keep the project this checkout already syncs under\n", what)
	return true
}

// authoredItems narrows a hosted-mode push's item rows to the stories this
// machine authored: those carrying ledger.MarkItemPending. Stories with events
// past their head (plan) are marked first, so work made before the mark existed
// still leaves. A marked story the cursor passed over is read and added; a story
// row whose updated_at moved with no local append is dropped. Rows of other kinds
// are kept as they came.
func authoredItems(ctx context.Context, a *app.App, items []json.RawMessage, plan []storyAppend, scope scopeSet) ([]json.RawMessage, error) {
	planned := make([]string, 0, len(plan))
	for _, p := range plan {
		planned = append(planned, p.id)
	}
	if err := a.Store.Ledger.SetMarks(ctx, ledger.MarkItemPending, planned); err != nil {
		return nil, err
	}
	pending, err := a.Store.Ledger.Marked(ctx, ledger.MarkItemPending)
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(items))
	have := map[string]bool{}
	for _, raw := range items {
		id, kind := rawItemKey(raw)
		if kind == string(workitem.KindStory) && !pending[id] {
			continue
		}
		have[id] = true
		out = append(out, raw)
	}
	for id := range pending {
		if have[id] || !scope.HasItem(id) {
			continue
		}
		it, gerr := a.Store.Stories.Get(ctx, id)
		if errors.Is(gerr, workitem.ErrNotFound) {
			continue
		}
		if gerr != nil {
			return nil, gerr
		}
		if it.Kind != workitem.KindStory {
			continue
		}
		raw, merr := marshalWorkstateItem(it)
		if merr != nil {
			return nil, merr
		}
		out = append(out, raw)
	}
	return out, nil
}

// clearAuthored clears the mark of every story whose row an accepted Apply carried.
func clearAuthored(ctx context.Context, a *app.App, items []json.RawMessage) error {
	var ids []string
	for _, raw := range items {
		if id, kind := rawItemKey(raw); kind == string(workitem.KindStory) {
			ids = append(ids, id)
		}
	}
	return a.Store.Ledger.ClearMarks(ctx, ledger.MarkItemPending, ids)
}

func rawItemKey(raw json.RawMessage) (id, kind string) {
	var w struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(raw, &w)
	return w.ID, w.Kind
}

// leaveRemote returns the applied items a pass folds under their chains: all of
// them, but in hosted mode not the stories whose history stays on the server.
// Each of those that holds no local event is marked ledger.MarkHistoryRemote, so
// opening it fetches its history.
func leaveRemote(ctx context.Context, a *app.App, chain *chainPull, applied []workitem.Item) ([]workitem.Item, error) {
	out := make([]workitem.Item, 0, len(applied))
	var remote []string
	for _, it := range applied {
		if !chain.remote(it) {
			out = append(out, it)
			continue
		}
		events, err := a.Store.Ledger.StoryChain(ctx, it.ID)
		if err != nil {
			return nil, fmt.Errorf("read the chain of %s: %w", it.ID, err)
		}
		if len(events) == 0 {
			remote = append(remote, it.ID)
		}
	}
	return out, a.Store.Ledger.SetMarks(ctx, ledger.MarkHistoryRemote, remote)
}

// holdHosted narrows c, in hosted mode, to the rows a newer local copy may stand
// for: only a story this machine authored keeps one; any other is the server's to
// replace. In team scope it also narrows the hosted chains a pass reads to the
// stories the local store holds and those whose hosted row is performing; outside
// team scope every chain is read.
func holdHosted(ctx context.Context, a *app.App, c *chainPull) error {
	if !verb.HostedMode(a.Config) {
		return nil
	}
	authored, err := a.Store.Ledger.Marked(ctx, ledger.MarkItemPending)
	if err != nil {
		return err
	}
	c.authored = authored
	if !teamScope(a) {
		return nil
	}
	held, err := verb.HeldStories(ctx, a.Store.Ledger, a.Store.Stories)
	if err != nil {
		return err
	}
	performing := verb.StoryPerforming(ctx)
	c.held = func(it workitem.Item) bool { return held[it.ID] || performing(it) }
	return nil
}

// teamScope reports whether the repo's recorded sync scope is team: the only
// scope whose local store trims story history (sty_7c01ca14). It reads the scope
// the sync pass already resolved (syncstate.RecordedScope) and makes no hosted
// call; no record, or one for another binding, is local and trims nothing.
func teamScope(a *app.App) bool {
	return syncstate.RecordedScope(config.GlobalDir(), a.RepoRoot, a.Config.SyncProject()) == config.TeamScope
}

// hostedIdentity is the hosted block of the mirror identity: nil unless in hosted
// mode, else the server, the bound project and the stories this machine engaged.
func hostedIdentity(ctx context.Context, a *app.App) *mirror.HostedMeta {
	if !verb.HostedMode(a.Config) {
		return nil
	}
	project, err := resolveBoundProject(a.Config, a.RepoRoot)
	if err != nil {
		return nil
	}
	set, err := verb.MachineSet(ctx, a.Store.Ledger, "")
	if err != nil {
		return nil
	}
	ids := slices.Sorted(maps.Keys(set))
	if ids == nil {
		ids = []string{}
	}
	return &mirror.HostedMeta{Server: config.ResolveHostedServer(a.Config), Project: project, MachineStories: ids}
}

// trimHostedHistory drops, in hosted team scope, the history of every story the
// local store need not hold and the server holds all of. It is a no-op otherwise.
func trimHostedHistory(ctx context.Context, a *app.App, server, project string) (int, error) {
	if !verb.HostedMode(a.Config) || !teamScope(a) {
		return 0, nil
	}
	held, err := verb.HeldStories(ctx, a.Store.Ledger, a.Store.Stories)
	if err != nil {
		return 0, err
	}
	cur, err := hosted.LoadWorkstateCursor(server, project, a.RepoRoot)
	if err != nil {
		return 0, fmt.Errorf("load story heads: %w", err)
	}
	return verb.TrimStoryHistory(ctx, a.Store.Ledger, held, cur.StoryHeads)
}
