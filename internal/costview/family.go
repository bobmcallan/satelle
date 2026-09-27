package costview

import (
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// FamilyCost is a root item's own Story plus every descendant found by
// walking parent_id (any depth) in items, folded into a family total. Each
// Children entry's Figures already includes THAT child's own descendants, so
// the caller need only sum the top-level slice to get the full subtree.
type FamilyCost struct {
	Root     Story   `json:"root"`
	Children []Story `json:"children,omitempty"`
	Total    Figures `json:"total"`
}

// Family walks items by ParentID starting at root.ID, any depth, each id
// visited once — never gated by a category literal such as "epic-parent": the
// walk finding at least one child is the whole trigger. It performs no I/O:
// items, entriesByID and clockFor are supplied by the caller (a verb loader
// that already listed the store).
//
// Total.ElapsedMs is NOT root's clock plus every child's clock summed — a
// parent open the whole time its children run would double- and
// triple-count the same wall-clock window. It is instead the family's wall
// span: the earliest engage across root and every descendant, to the latest
// terminal (or now) across the same set. Root.Figures.ElapsedMs always stays
// root's own clock.
func Family(root workitem.Item, items []workitem.Item, entriesByID map[string][]ledger.Entry, clockFor func(workitem.Item) Clock, now time.Time) FamilyCost {
	byParent := map[string][]workitem.Item{}
	for _, it := range items {
		if it.ParentID != "" {
			byParent[it.ParentID] = append(byParent[it.ParentID], it)
		}
	}

	rootStory := Own(root, entriesByID[root.ID], clockFor(root), now)

	visited := map[string]bool{root.ID: true}
	var walk func(parentID string) ([]Story, *Span)
	walk = func(parentID string) ([]Story, *Span) {
		var out []Story
		var span *Span
		for _, k := range byParent[parentID] {
			if visited[k.ID] {
				continue
			}
			visited[k.ID] = true
			own := Own(k, entriesByID[k.ID], clockFor(k), now)
			grandchildren, gcSpan := walk(k.ID)
			total := own.Figures
			for _, gc := range grandchildren {
				total = Fold(total, gc.Figures)
			}
			// This child's OWN displayed elapsed is the union of ITS span with its
			// descendants' spans, exactly like the family Total below — never the
			// Fold sum above, which double-counts overlapping wall-clock windows
			// and, when either side is the never-engaged sentinel (-1), corrupts
			// the other side's real duration by simple arithmetic. A nested epic
			// (a child that is itself a parent) needs this at every level, not
			// only at the top (sty_b8542a3a AC2/AC5 rework).
			kidSpan := UnionSpan(own.Span, gcSpan)
			total.ElapsedMs = elapsedFromSpan(kidSpan)
			own.Figures = total
			out = append(out, own)
			span = UnionSpan(span, kidSpan)
		}
		return out, span
	}
	children, childSpan := walk(root.ID)

	total := rootStory.Figures
	for _, c := range children {
		total = Fold(total, c.Figures)
	}
	if len(children) > 0 {
		total.ElapsedMs = elapsedFromSpan(UnionSpan(rootStory.Span, childSpan))
	}
	return FamilyCost{Root: rootStory, Children: children, Total: total}
}

// elapsedFromSpan derives an ElapsedMs figure from a (possibly nil) Span: -1
// (unavailable, per FormatDuration) when span is nil — nothing in this
// subtree ever engaged — else the span's End-Start duration (0 for a
// degenerate zero-length span, matching Own's own computation). Shared by
// every level of the family walk so a node's displayed elapsed and the family
// Total are derived identically, never independently.
func elapsedFromSpan(span *Span) int64 {
	switch {
	case span == nil:
		return -1
	case span.End.After(span.Start):
		return span.End.Sub(span.Start).Milliseconds()
	default:
		return 0
	}
}
