package verb

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// StoryActualFigures is one item's own computed cost — dollars, the fresh/
// output/cache split, and time — derived solely from its ledger rows
// (sty_8eae81ac). Never hand-entered: ComputeStoryActual is the only writer of
// the actual-* tags and the actual_recorded payload.
//
// CostUSD sums only rows that carry cost_usd; CostUnavailableRows counts the
// rest — an unpriced row is never folded in as a zero (mirrors StoryCost's own
// CostedRows/UncostedRows split, sty_c4df7376). CacheWrite is its own field,
// never folded into FreshInput or CacheRead. UnsplitTokens carries a legacy
// row's TokensIn recorded before the fresh/cache split existed.
//
// ElapsedMs is the story clock: first entry into an engaging state to first
// entry into a terminal state (by shape — never a park), or to now when the
// item is still open. AgentMs is DispatchMs (agent_invocation durations) plus
// DriverMs (driver_usage wall time), each also kept separately.
type StoryActualFigures struct {
	CostUSD             float64 `json:"cost_usd"`
	CostRows            int     `json:"cost_rows"`
	CostUnavailableRows int     `json:"cost_unavailable_rows"`

	FreshInput    int `json:"fresh_input"`
	Output        int `json:"output"`
	CacheRead     int `json:"cache_read"`
	CacheWrite    int `json:"cache_write"`
	UnsplitTokens int `json:"unsplit_tokens,omitempty"`

	ElapsedMs  int64 `json:"elapsed_ms"`
	AgentMs    int64 `json:"agent_ms"`
	DispatchMs int64 `json:"dispatch_ms"`
	DriverMs   int64 `json:"driver_ms"`
}

// ChildActual is one child item's own computed actual — its OWN figures
// folded with its own descendants' (sty_8eae81ac AC3), so a parent's rollup
// never has to re-descend it.
type ChildActual struct {
	ID      string             `json:"id"`
	Figures StoryActualFigures `json:"figures"`
}

// StoryActual is the computed actual for one item: Own is its own figures,
// Children is every child's own (subtree) figures found by parent_id — at any
// depth, each id visited once, never by a category literal (architecture
// revision A2) — and Total is Own plus every Children entry.
type StoryActual struct {
	StoryID  string             `json:"story_id"`
	Own      StoryActualFigures `json:"own"`
	Children []ChildActual      `json:"children,omitempty"`
	Total    StoryActualFigures `json:"total"`
}

// actualAccumulator folds ledger rows into StoryActualFigures. Kept as its own
// type (rather than inline in computeOwnActual) so a dollar/token/time field
// added to one accumulation point cannot silently miss the other.
type actualAccumulator struct {
	costUSD             float64
	costRows            int
	costUnavailableRows int

	freshInput, output, cacheRead, cacheWrite, unsplit int

	dispatchMs, driverMs int64
}

// addDispatchRow folds one agent_invocation row (already tool-permission-
// filtered by buildCostRow). DurationMs and cost fold in regardless of token
// usage availability — an agent still spent wall time and may still have a
// priced cost even when its provider reported no token split. Token figures
// fold in only from a measured row (sty_56aae77a's rule: never a zero for an
// unreported one).
func (a *actualAccumulator) addDispatchRow(row StoryCostRow) {
	a.dispatchMs += row.DurationMs
	if row.CostUSD != nil {
		a.costUSD += *row.CostUSD
		a.costRows++
	} else {
		a.costUnavailableRows++
	}
	if !row.UsageAvailable {
		return
	}
	if row.TokensInFresh > 0 || row.TokensCacheWrite > 0 || row.TokensCacheRead > 0 {
		a.freshInput += row.TokensInFresh
		a.cacheWrite += row.TokensCacheWrite
		a.cacheRead += row.TokensCacheRead
	} else {
		a.unsplit += row.TokensIn
	}
	a.output += row.TokensOut
}

// addDriverRow folds one driver_usage row. WallSeconds is real elapsed time
// recorded regardless of whether the harness's usage read succeeded, so it
// always folds in; the token/cost fields only ever carry non-zero values on
// an Available row (recordDriverUsage never sets them otherwise).
func (a *actualAccumulator) addDriverRow(d DriverUsagePayload) {
	a.driverMs += int64(d.WallSeconds * 1000)
	if d.CostUSD != nil {
		a.costUSD += *d.CostUSD
		a.costRows++
	} else {
		a.costUnavailableRows++
	}
	if !d.Available {
		return
	}
	a.freshInput += d.FreshInput
	a.output += d.Output
	a.cacheRead += d.CacheRead
	a.cacheWrite += d.CacheWrite
}

func (a *actualAccumulator) figures() StoryActualFigures {
	return StoryActualFigures{
		CostUSD:             a.costUSD,
		CostRows:            a.costRows,
		CostUnavailableRows: a.costUnavailableRows,
		FreshInput:          a.freshInput,
		Output:              a.output,
		CacheRead:           a.cacheRead,
		CacheWrite:          a.cacheWrite,
		UnsplitTokens:       a.unsplit,
		AgentMs:             a.dispatchMs + a.driverMs,
		DispatchMs:          a.dispatchMs,
		DriverMs:            a.driverMs,
	}
}

// addFigures sums two figures — used to fold a child's (subtree) figures into
// a parent's running total (AC3).
func addFigures(a, b StoryActualFigures) StoryActualFigures {
	return StoryActualFigures{
		CostUSD:             a.CostUSD + b.CostUSD,
		CostRows:            a.CostRows + b.CostRows,
		CostUnavailableRows: a.CostUnavailableRows + b.CostUnavailableRows,
		FreshInput:          a.FreshInput + b.FreshInput,
		Output:              a.Output + b.Output,
		CacheRead:           a.CacheRead + b.CacheRead,
		CacheWrite:          a.CacheWrite + b.CacheWrite,
		UnsplitTokens:       a.UnsplitTokens + b.UnsplitTokens,
		ElapsedMs:           a.ElapsedMs + b.ElapsedMs,
		AgentMs:             a.AgentMs + b.AgentMs,
		DispatchMs:          a.DispatchMs + b.DispatchMs,
		DriverMs:            a.DriverMs + b.DriverMs,
	}
}

// actualSpan is an item's own wall-clock window — engage to terminal (or
// now) — kept separate from StoryActualFigures.ElapsedMs so a family of items
// can be unioned into the family's wall span rather than summed (revision 2
// point 4: a parent open the whole time its children run must not report
// their overlapping clocks added together).
type actualSpan struct {
	Start time.Time
	End   time.Time
}

// unionSpan folds two spans into the one that covers both — the earliest
// Start and the latest End. Either side may be nil (no span to contribute,
// e.g. an item with no children); nil is the identity element.
func unionSpan(a, b *actualSpan) *actualSpan {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	start, end := a.Start, a.End
	if b.Start.Before(start) {
		start = b.Start
	}
	if b.End.After(end) {
		end = b.End
	}
	return &actualSpan{Start: start, End: end}
}

// computeOwnActual reads item's own ledger rows and returns its own
// StoryActualFigures (no child rollup) and its own actualSpan. The story
// clock is derived by shape, never by a status literal (architecture
// revision A1): it starts at the first status_transition INTO a non-terminal
// engaging state (storyStatusIsEngaging), falling back to item.CreatedAt when
// the item never recorded one, and ends at the first status_transition INTO a
// terminal state (targetIsTerminalStateOnly — a park/blocked transition does
// NOT stop the clock, since the story is still open), or now when it has not
// yet reached one.
func computeOwnActual(ctx context.Context, item workitem.Item) (StoryActualFigures, actualSpan, error) {
	ls, err := requireLedger()
	if err != nil {
		return StoryActualFigures{}, actualSpan{}, err
	}
	entries, err := ls.ListByStory(ctx, item.ID, "")
	if err != nil {
		return StoryActualFigures{}, actualSpan{}, err
	}

	acc := &actualAccumulator{}
	engageAt := item.CreatedAt
	haveEngage := false
	var terminalAt time.Time
	haveTerminal := false

	for _, e := range entries {
		switch e.Kind {
		case ledger.KindAgentInvocation:
			row, ok := buildCostRow(e)
			if !ok {
				continue
			}
			acc.addDispatchRow(row)
		case ledger.KindDriverUsage:
			if len(e.Payload) == 0 {
				continue
			}
			var d DriverUsagePayload
			if json.Unmarshal(e.Payload, &d) != nil {
				continue
			}
			acc.addDriverRow(d)
		case ledger.KindStatusTransition:
			var p struct {
				From string `json:"from"`
				To   string `json:"to"`
			}
			if json.Unmarshal(e.Payload, &p) != nil {
				continue
			}
			if !haveEngage {
				if engaging, ok := storyStatusIsEngaging(ctx, item, p.To); ok && engaging {
					engageAt, haveEngage = e.CreatedAt, true
				}
			}
			if !haveTerminal && targetIsTerminalStateOnly(ctx, item, p.To) {
				terminalAt, haveTerminal = e.CreatedAt, true
			}
		}
	}

	end := time.Now()
	if haveTerminal {
		end = terminalAt
	}
	figures := acc.figures()
	if end.After(engageAt) {
		figures.ElapsedMs = end.Sub(engageAt).Milliseconds()
	}
	return figures, actualSpan{Start: engageAt, End: end}, nil
}

// collectChildActuals walks item ids by parent_id, any depth, each id visited
// once (guarded by visited) — never by a category literal such as
// "epic-parent" (architecture revision A2). Each returned ChildActual.Figures
// is that child's OWN figures already folded with ITS OWN descendants, so the
// caller need only sum the top-level slice to get the full subtree. The
// returned span is every visited child's (and descendant's) own span, unioned
// — the family's wall-clock window, for the caller to fold into a family span
// rather than a sum (revision 2 point 4); nil when there are no children.
func collectChildActuals(ctx context.Context, wi *workitem.Store, parentID string, visited map[string]bool) ([]ChildActual, *actualSpan, error) {
	kids, err := wi.List(ctx, workitem.ListFilter{ParentID: parentID, Limit: 2000})
	if err != nil {
		return nil, nil, err
	}
	var out []ChildActual
	var span *actualSpan
	for _, k := range kids {
		if visited[k.ID] {
			continue
		}
		visited[k.ID] = true
		own, ownSpan, err := computeOwnActual(ctx, k)
		if err != nil {
			return nil, nil, err
		}
		grandchildren, gcSpan, err := collectChildActuals(ctx, wi, k.ID, visited)
		if err != nil {
			return nil, nil, err
		}
		total := own
		for _, gc := range grandchildren {
			total = addFigures(total, gc.Figures)
		}
		out = append(out, ChildActual{ID: k.ID, Figures: total})
		kidSpan := ownSpan
		span = unionSpan(span, unionSpan(&kidSpan, gcSpan))
	}
	return out, span, nil
}

// ComputeStoryActual computes storyID's actual from the ledger alone — its own
// figures, every child's (by parent_id, any depth) folded in Children, and
// Total = Own + every Children entry (sty_8eae81ac AC1/AC2/AC3). A hand-typed
// actual-* tag never substitutes for this: recordActual (workitem.go) is the
// sole writer, called after a terminal transition commits.
//
// Total.ElapsedMs is NOT own's clock plus every child's clock summed — a
// parent open the whole time its children run would double- and triple-count
// the same wall-clock window. When the item has children, Total.ElapsedMs is
// instead the family's wall span: the earliest engage across the item and
// every descendant, to the latest terminal (or now) across the same set
// (revision 2 point 4). Own.ElapsedMs always stays the item's own clock.
func ComputeStoryActual(ctx context.Context, storyID string) (StoryActual, error) {
	wi, err := requireWorkItem()
	if err != nil {
		return StoryActual{}, err
	}
	item, err := wi.Get(ctx, storyID)
	if err != nil {
		return StoryActual{}, err
	}

	own, ownSpan, err := computeOwnActual(ctx, item)
	if err != nil {
		return StoryActual{}, err
	}

	result := StoryActual{StoryID: storyID, Own: own, Total: own}
	children, childSpan, err := collectChildActuals(ctx, wi, storyID, map[string]bool{storyID: true})
	if err != nil {
		return StoryActual{}, err
	}
	result.Children = children
	for _, c := range children {
		result.Total = addFigures(result.Total, c.Figures)
	}
	if len(children) > 0 {
		family := unionSpan(&ownSpan, childSpan)
		if family != nil && family.End.After(family.Start) {
			result.Total.ElapsedMs = family.End.Sub(family.Start).Milliseconds()
		}
	}
	return result, nil
}
