package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/worktree"
)

// Work-state push holds a story's activity until the code it describes can be
// checked out from the git remote (sty_7361569a), so another machine never sees
// a status it cannot act on. The decision is made at push time from live git
// state, so nothing is dropped: a held row stays behind the cursor and the next
// push re-checks. The hold is mechanism; whether it runs is [sync] hold_unpushed.

// ledgerRowMeta is what the hold needs to know about one collected ledger row,
// parallel to the batch's raw rows: whose it is and where it sits.
type ledgerRowMeta struct {
	storyID   string
	id        string
	seq       int64
	createdAt time.Time
}

// holdResult is the outcome of classifying a batch.
type holdResult struct {
	held        map[string]string // story id -> reason
	unavailable map[string]string // story id -> why reachability could not be checked
}

// lines renders one line per held story, then one per story not checked.
func (h holdResult) lines() []string {
	var out []string
	for _, id := range slices.Sorted(maps.Keys(h.held)) {
		out = append(out, fmt.Sprintf("held %s: %s", id, h.held[id]))
	}
	for _, id := range slices.Sorted(maps.Keys(h.unavailable)) {
		out = append(out, fmt.Sprintf("reachability unavailable (%s): %s not checked", h.unavailable[id], id))
	}
	return out
}

// treeProbe is one engagement tree's relation to the remote, asked once per push.
type treeProbe struct {
	dirty bool
	head  string
	// onRemote is true when a remote-tracking ref contains head.
	onRemote bool
	err      error
}

func probeTree(tree string) treeProbe {
	dirty, err := worktree.Dirty(tree)
	if err != nil {
		return treeProbe{err: err}
	}
	head, err := worktree.Head(tree)
	if err != nil {
		return treeProbe{err: err}
	}
	on, err := worktree.OnRemote(tree, head)
	return treeProbe{dirty: dirty, head: head, onRemote: on, err: err}
}

// classifyHolds decides which stories in the batch are held. Only stories the
// batch names are looked at, as an item row or as the owner of a ledger row.
func classifyHolds(ctx context.Context, a *app.App, items []json.RawMessage, rows []ledgerRowMeta) (holdResult, error) {
	res := holdResult{held: map[string]string{}, unavailable: map[string]string{}}
	ids := map[string]bool{}
	for _, raw := range items {
		if id := rawItemID(raw); id != "" {
			ids[id] = true
		}
	}
	for _, r := range rows {
		if r.storyID != "" {
			ids[r.storyID] = true
		}
	}
	probes := map[string]treeProbe{}
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		it, err := a.Store.Stories.Get(ctx, id)
		if err != nil {
			continue // not a work item on this machine: nothing to hold it by
		}
		st, err := verb.StoryCodeState(ctx, it, a.RepoRoot)
		if err != nil {
			return res, fmt.Errorf("hold check %s: %w", id, err)
		}
		if st.Unavailable != "" {
			res.unavailable[id] = st.Unavailable
			continue
		}
		if !st.Eligible {
			continue
		}
		p, ok := probes[st.Tree]
		if !ok {
			p = probeTree(st.Tree)
			probes[st.Tree] = p
		}
		switch {
		case p.err != nil:
			res.unavailable[id] = p.err.Error()
		case p.dirty:
			res.held[id] = fmt.Sprintf("uncommitted changes in %s", st.Tree)
		case !p.onRemote:
			res.held[id] = fmt.Sprintf("head %s is not on any remote branch", short8(p.head))
		}
	}
	return res, nil
}

func short8(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// withoutHeld drops the held stories' item rows and ledger rows. It returns the
// rows left, the ledger metadata for what is left, and the earliest updated_at
// among the held item rows (zero when none), which is how far the items cursor
// may safely advance.
func withoutHeld(held map[string]string, items, ledgerRaw []json.RawMessage, rows []ledgerRowMeta) (keepItems, keepLedger []json.RawMessage, earliestHeld time.Time) {
	keepItems = make([]json.RawMessage, 0, len(items))
	for _, raw := range items {
		id := rawItemID(raw)
		if _, h := held[id]; !h {
			keepItems = append(keepItems, raw)
			continue
		}
		var w struct {
			UpdatedAt time.Time `json:"updated_at"`
		}
		_ = json.Unmarshal(raw, &w)
		if earliestHeld.IsZero() || w.UpdatedAt.Before(earliestHeld) {
			earliestHeld = w.UpdatedAt
		}
	}
	keepLedger = make([]json.RawMessage, 0, len(ledgerRaw))
	for i, raw := range ledgerRaw {
		if i < len(rows) {
			if _, h := held[rows[i].storyID]; h {
				continue
			}
		}
		keepLedger = append(keepLedger, raw)
	}
	return keepItems, keepLedger, earliestHeld
}

// itemsCursorBefore is the items cursor to save when rows were held: the newest
// kept item that predates every held one, else the old cursor. Anything newer is
// re-collected next push; ingest is idempotent by id.
func itemsCursorBefore(kept []json.RawMessage, earliestHeld, old time.Time) time.Time {
	best := old
	for _, raw := range kept {
		var w struct {
			UpdatedAt time.Time `json:"updated_at"`
		}
		if json.Unmarshal(raw, &w) == nil && w.UpdatedAt.Before(earliestHeld) && w.UpdatedAt.After(best) {
			best = w.UpdatedAt
		}
	}
	return best
}

// ledgerMarkBeforeHold is the ledger position to save when rows were held: the
// row just before the first held one, so that row and everything after it is
// collected again next push. start is the position the collection began from.
func ledgerMarkBeforeHold(start ledgerMark, held map[string]string, rows []ledgerRowMeta) ledgerMark {
	m := start
	for _, r := range rows {
		if _, h := held[r.storyID]; h {
			break
		}
		if r.createdAt.After(m.createdAt) {
			m.createdAt = r.createdAt
		}
		m.seq = r.seq
		m.anchorID = r.id
	}
	return m
}
