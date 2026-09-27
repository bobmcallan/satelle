package verb

import (
	"context"
	"time"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// StoryActualFigures is costview's Figures — kept as a verb-local alias so
// every existing caller (JSON field names, the actual-* tag writer) is
// unaffected by the move. costview is the SOLE owner of this computation
// (sty_b8542a3a): measured_actual, the computed actual-* tags, `satelle story
// cost` and the web page all read the same numbers off it.
type StoryActualFigures = costview.Figures

// ChildActual is one child item's own computed actual — its OWN figures
// folded with its own descendants' — so a parent's rollup never has to
// re-descend it.
type ChildActual struct {
	ID      string             `json:"id"`
	Figures StoryActualFigures `json:"figures"`
}

// StoryActual is the computed actual for one item: Own is its own figures,
// Children is every child's own (subtree) figures found by parent_id — at any
// depth, each id visited once, never by a category literal — and Total is Own
// plus every Children entry.
type StoryActual struct {
	StoryID  string             `json:"story_id"`
	Own      StoryActualFigures `json:"own"`
	Children []ChildActual      `json:"children,omitempty"`
	Total    StoryActualFigures `json:"total"`
}

// clockFor resolves item's governing workflow and returns the costview.Clock
// wfgovern.ClockFor derives from its shape. ok is false when governance
// cannot be resolved — the caller then treats the item as having no
// shape-derived clock, exactly as before this seam existed.
func clockFor(ctx context.Context, item workitem.Item) (costview.Clock, bool) {
	idx, err := requireDocIndex()
	if err != nil {
		return costview.Clock{}, false
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return costview.Clock{}, false
	}
	return wfgovern.ClockFor(wfs, item)
}

// collectDescendants walks item ids by parent_id, any depth, each id visited
// once — never by a category literal such as "epic-parent". It returns every
// visited descendant Item (NOT including root), for costview.Family to walk
// in memory. Kind is pinned to story: the web side (mirror_panels.go
// mirrorBuildCostVM) walks the same family over decodeItems(..., "story")
// only, so a task or execution child would otherwise show up in the CLI's
// family table and total but never in the web page's (sty_b8542a3a AC7
// rework) — the two surfaces must walk the same child set to report the
// same family.
func collectDescendants(ctx context.Context, wi *workitem.Store, parentID string, visited map[string]bool, out *[]workitem.Item) error {
	kids, err := wi.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, ParentID: parentID, Limit: 2000})
	if err != nil {
		return err
	}
	for _, k := range kids {
		if visited[k.ID] {
			continue
		}
		visited[k.ID] = true
		*out = append(*out, k)
		if err := collectDescendants(ctx, wi, k.ID, visited, out); err != nil {
			return err
		}
	}
	return nil
}

// computeFamilyCost gathers root and every descendant found by walking
// parent_id (any depth, never gated by a category literal), fetches each
// one's ledger entries, and folds them through costview.Family. It returns
// nil when root has no children — the family section's whole trigger,
// consistent whether the caller is ComputeStoryActual or the cost view (AC5).
func computeFamilyCost(ctx context.Context, wi *workitem.Store, ls *ledger.Store, root workitem.Item) (*costview.FamilyCost, error) {
	var descendants []workitem.Item
	if err := collectDescendants(ctx, wi, root.ID, map[string]bool{root.ID: true}, &descendants); err != nil {
		return nil, err
	}
	if len(descendants) == 0 {
		return nil, nil
	}

	items := append([]workitem.Item{root}, descendants...)
	entries := map[string][]ledger.Entry{}
	for _, it := range items {
		es, err := ls.ListByStory(ctx, it.ID, "")
		if err != nil {
			return nil, err
		}
		entries[it.ID] = es
	}

	family := costview.Family(root, items, entries, func(it workitem.Item) costview.Clock {
		clk, _ := clockFor(ctx, it)
		return clk
	}, time.Now())
	return &family, nil
}

// ComputeStoryActual computes storyID's actual from the ledger alone via
// costview — its own figures, every child's (by parent_id, any depth) folded
// in Children, and Total = Own plus every Children entry. A hand-typed
// actual-* tag never substitutes for this: recordActual (workitem.go) is the
// sole writer, called after a terminal transition commits.
func ComputeStoryActual(ctx context.Context, storyID string) (StoryActual, error) {
	wi, err := requireWorkItem()
	if err != nil {
		return StoryActual{}, err
	}
	ls, err := requireLedger()
	if err != nil {
		return StoryActual{}, err
	}
	item, err := wi.Get(ctx, storyID)
	if err != nil {
		return StoryActual{}, err
	}
	entries, err := ls.ListByStory(ctx, storyID, "")
	if err != nil {
		return StoryActual{}, err
	}

	clk, _ := clockFor(ctx, item)
	own := costview.Own(item, entries, clk, time.Now())

	result := StoryActual{StoryID: storyID, Own: own.Figures, Total: own.Figures}
	family, err := computeFamilyCost(ctx, wi, ls, item)
	if err != nil {
		return StoryActual{}, err
	}
	if family != nil {
		result.Total = family.Total
		for _, c := range family.Children {
			result.Children = append(result.Children, ChildActual{ID: c.ID, Figures: c.Figures})
		}
	}
	return result, nil
}
