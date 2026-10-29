// Package throughput computes what `satelle stats` reports: how many stories and
// epics finished in a window, how long each phase of their cycle took, and how
// long they held each status. Every figure is derived from rows satelle already
// records — a story's story_created and status_transition ledger rows and its
// record — and a figure those rows cannot support is unavailable, never zero.
package throughput

import (
	"encoding/json"
	"math"
	"sort"
	"time"

	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/storyfold"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Phases is one finished item's cycle, split at the route's pull-request step.
type Phases struct {
	// Start is the item's entry to the route's start state; zero when no row
	// records it.
	Start time.Time
	// Boundary is the first entry to a state the route marks pr_step; zero when
	// the item never entered one. A later loop back past it does not move it.
	Boundary time.Time
	// End is the final entry to a terminal state.
	End time.Time
	// Phase2Applicable is false when the route marks no pr_step state: the whole
	// start-to-terminal time is then phase 1.
	Phase2Applicable bool
}

// Phase1 is the time from start to the pull-request step (or to the terminal
// state on a route with none), and whether the rows record both ends.
func (p Phases) Phase1() (time.Duration, bool) {
	end := p.End
	if p.Phase2Applicable {
		end = p.Boundary
	}
	if p.Start.IsZero() || end.IsZero() {
		return 0, false
	}
	return end.Sub(p.Start), true
}

// Phase2 is the time from the pull-request step to the terminal state, and
// whether it is recorded. It is never recorded on a route with no pr_step.
func (p Phases) Phase2() (time.Duration, bool) {
	if !p.Phase2Applicable || p.Boundary.IsZero() {
		return 0, false
	}
	return p.End.Sub(p.Boundary), true
}

// Cycle splits an item's rows into its Phases on spec, its governing route. ok is
// false when the item's last status_transition is not into a terminal state.
// With no resolved route (a zero Spec) the store's own defaults stand in: the
// item starts in backlog, finishes in done, and has no pull-request step.
func Cycle(entries []ledger.Entry, spec wfdot.Spec) (Phases, bool) {
	es := append([]ledger.Entry(nil), entries...)
	ledger.SortEntries(es)
	start := spec.Start()
	if start == "" {
		start = workitem.StatusBacklog
	}
	p := Phases{Phase2Applicable: spec.HasPRState()}
	var last string
	var lastAt time.Time
	for _, e := range es {
		switch e.Kind {
		case ledger.KindStoryCreated:
			if p.Start.IsZero() && createdIn(e) == start {
				p.Start = e.CreatedAt
			}
		case ledger.KindStatusTransition:
			to := storyfold.TransitionTo(e)
			if p.Start.IsZero() && to == start {
				p.Start = e.CreatedAt
			}
			if p.Boundary.IsZero() && spec.IsPRState(to) {
				p.Boundary = e.CreatedAt
			}
			last, lastAt = to, e.CreatedAt
		}
	}
	if last == "" || !terminal(spec, last) {
		return Phases{}, false
	}
	p.End = lastAt
	return p, true
}

// createdIn is the status a story_created row records the item entering. A row
// with none predates the field; the store creates every such item in backlog.
func createdIn(e ledger.Entry) string {
	var p struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(e.Payload, &p)
	if p.Status == "" {
		return workitem.StatusBacklog
	}
	return p.Status
}

func terminal(spec wfdot.Spec, status string) bool {
	if len(spec.States) > 0 {
		return spec.IsTerminalState(status)
	}
	return status == workitem.StatusDone
}

// Item is one finished story or epic, reduced to what the report aggregates.
type Item struct {
	ID       string
	Epic     bool
	Finished time.Time
	Phases   Phases
	// Dwell is the wall time the item held each status (verb.StateDwell).
	Dwell map[string]time.Duration
	// Spec is the item's governing route; it orders the quality rows.
	Spec wfdot.Spec
	// Rows are the item's verdict and agent_invocation ledger rows, oldest
	// first: what the quality figures are counted from.
	Rows []ledger.Entry
}

// Finish reduces a story's rows to an Item, and reports whether it finished.
func Finish(it workitem.Item, entries []ledger.Entry, spec wfdot.Spec) (Item, bool) {
	p, ok := Cycle(entries, spec)
	if !ok {
		return Item{}, false
	}
	es := append([]ledger.Entry(nil), entries...)
	ledger.SortEntries(es)
	return Item{
		ID:       it.ID,
		Epic:     epicset.IsEpicParent(it),
		Finished: p.End,
		Phases:   p,
		Dwell:    verb.StateDwell(es),
		Spec:     spec,
	}, true
}

// Percentile is the nearest-rank p-th percentile (0 < p <= 1) of ds: the
// smallest value at least p of the population does not exceed. ds must be
// non-empty; it is not modified.
func Percentile(ds []time.Duration, p float64) time.Duration {
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	rank := int(math.Ceil(p * float64(len(s))))
	return s[min(max(rank, 1), len(s))-1]
}

// Median is the nearest-rank median: the lower middle value of an even population.
func Median(ds []time.Duration) time.Duration { return Percentile(ds, 0.5) }

// P90 is the nearest-rank 90th percentile.
func P90(ds []time.Duration) time.Duration { return Percentile(ds, 0.9) }
