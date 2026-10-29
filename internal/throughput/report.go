package throughput

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/bobmcallan/satelle/internal/costview"
)

// The reasons a figure is unavailable. Each is said in place of the figure.
const (
	ReasonNoFinished   = "no finished items in window"
	ReasonNotRecorded  = "not recorded"
	ReasonNoPRStep     = "no step on the route is marked pr_step"
	ReasonNoRepo       = "no readable repo"
	ReasonNoEstimate   = "no estimate"
	ReasonZeroEstimate = "estimate is zero"
	ReasonNoRatio      = "no item with both an estimate and a measured actual"
	ReasonUnsplit      = "no row recorded the fresh/cache split"

	ReasonNoPresentation = "no gated presentation recorded"
	ReasonNoExitReject   = "no exit rejection recorded"
	ReasonNoReject       = "no reviewer reject recorded"
	ReasonNoCreated      = "no story created in window"
)

// Window is the reporting window, in UTC.
type Window struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
}

// Contains reports whether t falls inside the window, both ends included.
func (w Window) Contains(t time.Time) bool { return !t.Before(w.Since) && !t.After(w.Until) }

// Count is a counted figure. Value is nil when it could not be counted, and
// Reason then says why; a real zero is a non-nil 0.
type Count struct {
	Value  *int   `json:"value"`
	Reason string `json:"reason,omitempty"`
}

// Duration is a timed figure in seconds. Seconds is nil when it is unavailable
// or not applicable, and Reason then says why.
type Duration struct {
	Seconds       *float64 `json:"seconds"`
	NotApplicable bool     `json:"not_applicable,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

// Phase is one cycle phase's figures over the items that record it.
type Phase struct {
	Items  int      `json:"items"`
	Median Duration `json:"median"`
	P90    Duration `json:"p90"`
}

// Bucket is the finished count for one UTC day (2006-01-02) or ISO week
// (2006-W01).
type Bucket struct {
	Key     string `json:"key"`
	Stories Count  `json:"stories"`
	Epics   Count  `json:"epics"`
}

// Step is the median time finished items held one status, over the items that
// held it.
type Step struct {
	Status string   `json:"status"`
	Items  int      `json:"items"`
	Median Duration `json:"median"`
}

// Steps is the per-status medians, in status order. Reason is set when there
// are none to give.
type Steps struct {
	Rows   []Step `json:"rows"`
	Reason string `json:"reason,omitempty"`
}

// Cost is a derived cost in USD under the operator's [pricing]. USD is nil when
// no part could be priced, and Reason then says why; Derived is costview's
// figure behind it, naming any part left unpriced beside a priced USD.
type Cost struct {
	USD     *float64             `json:"usd"`
	Reason  string               `json:"reason,omitempty"`
	Derived costview.DerivedCost `json:"derived_cost"`
}

// Usage is measured tokens by class and what they cost. A class no row measured
// is unavailable, never 0.
type Usage struct {
	FreshInput Count `json:"fresh_input"`
	CacheRead  Count `json:"cache_read"`
	CacheWrite Count `json:"cache_write"`
	Output     Count `json:"output"`
	// UnsplitInput is input that legacy rows recorded with no fresh/cache split;
	// set only when such a row was folded in.
	UnsplitInput *Count `json:"unsplit_input,omitempty"`
	Cost         Cost   `json:"cost"`
}

// Breakdown is one seat's (agent's) or one model's share of a Usage.
type Breakdown struct {
	Name string `json:"name"`
	Usage
}

// EstimateVsActual is one estimated unit of an item beside its measured actual
// (costview.ActualFor). Actual is nil when it was not measured and Ratio, the
// actual÷estimate, is nil when it cannot be taken; Reason then says why.
type EstimateVsActual struct {
	Unit     string   `json:"unit"`
	Estimate float64  `json:"estimate"`
	Actual   *int     `json:"actual"`
	Ratio    *float64 `json:"ratio"`
	Reason   string   `json:"reason,omitempty"`
}

// StoryCost is one finished item's own cost, by seat and by model, and how close
// its estimate was. EstimatesReason is set when it carries no estimate.
type StoryCost struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Repo     string    `json:"repo,omitempty"`
	Finished time.Time `json:"finished"`
	Usage
	BySeat          []Breakdown        `json:"by_seat"`
	ByModel         []Breakdown        `json:"by_model"`
	Estimates       []EstimateVsActual `json:"estimates"`
	EstimatesReason string             `json:"estimates_reason,omitempty"`
}

// CostSection is what the items finished in the window cost: in sum, by seat,
// by model, and per item. Seats and models are ordered by cost, highest first.
type CostSection struct {
	Usage
	BySeat  []Breakdown `json:"by_seat"`
	ByModel []Breakdown `json:"by_model"`
	Stories []StoryCost `json:"stories"`
}

// Spread is how actual÷estimate fell, for one unit, across the items recording
// both figures (Stories); Unavailable counts the finished items left out.
type Spread struct {
	Unit        string `json:"unit"`
	Stories     Count  `json:"stories"`
	Unavailable Count  `json:"unavailable"`
	Median      Ratio  `json:"median"`
	P90         Ratio  `json:"p90"`
	Min         Ratio  `json:"min"`
	Max         Ratio  `json:"max"`
}

// RepoReport is one repo's figures, or the total's.
type RepoReport struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
	// Error is why the repo could not be read; every figure is then unavailable
	// and the repo is left out of the total.
	Error string `json:"error,omitempty"`
	// Excluded names the repos left out of the total because they could not be
	// read. Set on the total only.
	Excluded []string      `json:"excluded,omitempty"`
	Stories  Count         `json:"stories"`
	Epics    Count         `json:"epics"`
	Days     []Bucket      `json:"days"`
	Weeks    []Bucket      `json:"weeks"`
	Phase1   Phase         `json:"phase1"`
	Phase2   Phase         `json:"phase2"`
	Steps    Steps         `json:"steps"`
	Quality  QualityReport `json:"quality"`
	Cost     CostSection   `json:"cost"`
	Spread   []Spread      `json:"estimate_spread"`
}

// Report is everything `satelle stats` prints. The text renderer and the JSON
// encoder both read it, so the two forms cannot disagree.
type Report struct {
	Window Window       `json:"window"`
	Repos  []RepoReport `json:"repos"`
	Total  RepoReport   `json:"total"`
}

func counted(n int) Count { return Count{Value: &n} }

func timed(d time.Duration) Duration {
	s := d.Seconds()
	return Duration{Seconds: &s}
}

func unavailable(reason string) Duration { return Duration{Reason: reason} }

func notApplicable() Duration { return Duration{NotApplicable: true, Reason: ReasonNoPRStep} }

// Summarise aggregates the finished items of one repo (or of every readable
// repo, for the total) over w, and counts work that came back from ret.
func Summarise(name, path string, items []Item, w Window, ret Returns) RepoReport {
	r := RepoReport{Name: name, Path: path}
	stories, epics := 0, 0
	day, week := map[string][2]int{}, map[string][2]int{}
	for _, it := range items {
		k := 0
		if it.Epic {
			k = 1
			epics++
		} else {
			stories++
		}
		d, wk := dayKey(it.Finished), weekKey(it.Finished)
		dc, wc := day[d], week[wk]
		dc[k]++
		wc[k]++
		day[d], week[wk] = dc, wc
	}
	r.Stories, r.Epics = counted(stories), counted(epics)
	bucket := func(key string, n [2]int) Bucket {
		return Bucket{Key: key, Stories: counted(n[0]), Epics: counted(n[1])}
	}
	for _, k := range dayKeys(w) {
		r.Days = append(r.Days, bucket(k, day[k]))
	}
	for _, k := range weekKeys(w) {
		r.Weeks = append(r.Weeks, bucket(k, week[k]))
	}
	r.Phase1, r.Phase2 = phases(items)
	r.Steps = steps(items)
	r.Quality = Quality(items, ret)
	r.Cost = costs(items)
	r.Spread = spreads(r.Cost.Stories)
	return r
}

// costs folds each item's own figures and seat lines with costview's counting
// rule, so the sum, the seats and the models of a section always agree.
func costs(items []Item) CostSection {
	c := CostSection{BySeat: []Breakdown{}, ByModel: []Breakdown{}, Stories: []StoryCost{}}
	if len(items) == 0 {
		c.Usage = unavailableUsage(ReasonNoFinished)
		return c
	}
	var total costview.Figures
	var seats []costview.SeatLine
	for _, it := range items {
		total = costview.Fold(total, it.Own.Figures)
		seats = costview.MergeSeats(seats, it.Own.Seats)
		c.Stories = append(c.Stories, storyCost(it))
	}
	sort.SliceStable(c.Stories, func(i, j int) bool {
		a, b := c.Stories[i], c.Stories[j]
		if !a.Finished.Equal(b.Finished) {
			return a.Finished.Before(b.Finished)
		}
		return a.Repo+a.ID < b.Repo+b.ID
	})
	c.Usage = usageOf(total)
	c.BySeat, c.ByModel = breakdowns(seats)
	return c
}

func storyCost(it Item) StoryCost {
	s := StoryCost{ID: it.ID, Title: it.Title, Repo: it.Repo, Finished: it.Finished, Usage: usageOf(it.Own.Figures)}
	s.BySeat, s.ByModel = breakdowns(it.Own.Seats)
	s.Estimates = estimates(it.Own)
	if len(s.Estimates) == 0 {
		s.EstimatesReason = ReasonNoEstimate
	}
	return s
}

// breakdowns groups seat lines by their agent and by their model.
func breakdowns(seats []costview.SeatLine) (bySeat, byModel []Breakdown) {
	agent := func(l costview.SeatLine) string { return nameOr(l.Agent, "unknown") }
	model := func(l costview.SeatLine) string { return nameOr(l.Model, "unrecorded") }
	return group(seats, agent), group(seats, model)
}

func group(seats []costview.SeatLine, key func(costview.SeatLine) string) []Breakdown {
	by := map[string]costview.Figures{}
	for _, l := range seats {
		by[key(l)] = costview.Fold(by[key(l)], l.Figures())
	}
	out := make([]Breakdown, 0, len(by))
	for name, f := range by {
		out = append(out, Breakdown{Name: name, Usage: usageOf(f)})
	}
	usd := func(b Breakdown) float64 {
		if b.Cost.USD == nil {
			return -1
		}
		return *b.Cost.USD
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := usd(out[i]), usd(out[j]); a != b {
			return a > b
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func nameOr(name, none string) string {
	if name == "" {
		return none
	}
	return name
}

// usageOf reads f's token classes the way costview's formatters do: output needs
// a measured row, and fresh input and the cache classes a measured row that
// recorded the split.
func usageOf(f costview.Figures) Usage {
	unmeasured := ReasonNotRecorded
	if f.UsageUnavailableRows > 0 {
		unmeasured = fmt.Sprintf("%s: %d rows reported no usage", ReasonNotRecorded, f.UsageUnavailableRows)
	}
	splitWhy := unmeasured
	if f.UsageRows > 0 {
		splitWhy = ReasonUnsplit
	}
	split := f.UsageRows-f.UnsplitRows > 0
	u := Usage{
		FreshInput: tokens(f.FreshInput, split, splitWhy),
		CacheRead:  tokens(f.CacheRead, split, splitWhy),
		CacheWrite: tokens(f.CacheWrite, split, splitWhy),
		Output:     tokens(f.Output, f.UsageRows > 0, unmeasured),
		Cost:       costOf(f.Derived),
	}
	if f.UnsplitRows > 0 {
		n := counted(f.UnsplitTokens)
		u.UnsplitInput = &n
	}
	return u
}

func tokens(n int, measured bool, reason string) Count {
	if !measured {
		return Count{Reason: reason}
	}
	return counted(n)
}

func costOf(d costview.DerivedCost) Cost {
	if why := d.UnavailableReason(); why != "" {
		return Cost{Reason: why, Derived: d}
	}
	usd := d.USD
	return Cost{USD: &usd, Derived: d}
}

func unavailableUsage(reason string) Usage {
	none := Count{Reason: reason}
	return Usage{FreshInput: none, CacheRead: none, CacheWrite: none, Output: none, Cost: Cost{Reason: reason}}
}

// estimates pairs each unit the item estimated with its own measured actual.
func estimates(own costview.Story) []EstimateVsActual {
	out := []EstimateVsActual{}
	for _, unit := range costview.EstimateUnits() {
		e, ok := costview.EstimateFor(own.Estimates, unit)
		if !ok {
			continue
		}
		ev := EstimateVsActual{Unit: unit, Estimate: e.Value}
		actual, measured := costview.ActualFor(unit, own.Figures)
		switch {
		case !measured:
			ev.Reason = ReasonNotRecorded
		case e.Value == 0:
			ev.Actual, ev.Reason = &actual, ReasonZeroEstimate
		default:
			ratio := float64(actual) / e.Value
			ev.Actual, ev.Ratio = &actual, &ratio
		}
		out = append(out, ev)
	}
	return out
}

// spreads is, per estimate unit, the nearest-rank spread of actual÷estimate over
// the items that record both figures.
func spreads(stories []StoryCost) []Spread {
	out := []Spread{}
	for _, unit := range costview.EstimateUnits() {
		var ratios []float64
		for _, s := range stories {
			for _, e := range s.Estimates {
				if e.Unit == unit && e.Ratio != nil {
					ratios = append(ratios, *e.Ratio)
				}
			}
		}
		sp := Spread{Unit: unit, Stories: counted(len(ratios)), Unavailable: counted(len(stories) - len(ratios))}
		if len(ratios) == 0 {
			why := ReasonNoRatio
			if len(stories) == 0 {
				why = ReasonNoFinished
			}
			none := Ratio{Reason: why}
			sp.Median, sp.P90, sp.Min, sp.Max = none, none, none, none
		} else {
			sp.Median, sp.P90 = ratioOf(nearestRank(ratios, 0.5)), ratioOf(nearestRank(ratios, 0.9))
			sp.Min, sp.Max = ratioOf(slices.Min(ratios)), ratioOf(slices.Max(ratios))
		}
		out = append(out, sp)
	}
	return out
}

func ratioOf(v float64) Ratio { return Ratio{Value: &v} }

func phases(items []Item) (Phase, Phase) {
	if len(items) == 0 {
		none := Phase{Median: unavailable(ReasonNoFinished), P90: unavailable(ReasonNoFinished)}
		return none, none
	}
	var one, two []time.Duration
	applicable := false
	for _, it := range items {
		if d, ok := it.Phases.Phase1(); ok {
			one = append(one, d)
		}
		if !it.Phases.Phase2Applicable {
			continue
		}
		applicable = true
		if d, ok := it.Phases.Phase2(); ok {
			two = append(two, d)
		}
	}
	p2 := figures(two)
	if !applicable {
		p2 = Phase{Median: notApplicable(), P90: notApplicable()}
	}
	return figures(one), p2
}

func figures(ds []time.Duration) Phase {
	if len(ds) == 0 {
		return Phase{Median: unavailable(ReasonNotRecorded), P90: unavailable(ReasonNotRecorded)}
	}
	return Phase{Items: len(ds), Median: timed(Median(ds)), P90: timed(P90(ds))}
}

func steps(items []Item) Steps {
	if len(items) == 0 {
		return Steps{Rows: []Step{}, Reason: ReasonNoFinished}
	}
	by := map[string][]time.Duration{}
	for _, it := range items {
		for status, d := range it.Dwell {
			by[status] = append(by[status], d)
		}
	}
	if len(by) == 0 {
		return Steps{Rows: []Step{}, Reason: ReasonNotRecorded}
	}
	names := make([]string, 0, len(by))
	for s := range by {
		names = append(names, s)
	}
	sort.Strings(names)
	out := Steps{Rows: []Step{}}
	for _, s := range names {
		out.Rows = append(out.Rows, Step{Status: s, Items: len(by[s]), Median: timed(Median(by[s]))})
	}
	return out
}

// Unavailable is a report whose every figure is unavailable for reason: a repo
// that could not be read, or a total with no readable repo behind it.
func Unavailable(name, path, reason string, w Window) RepoReport {
	none := Count{Reason: reason}
	phase := Phase{Median: unavailable(reason), P90: unavailable(reason)}
	r := RepoReport{
		Name: name, Path: path,
		Stories: none, Epics: none,
		Phase1: phase, Phase2: phase,
		Steps:   Steps{Rows: []Step{}, Reason: reason},
		Quality: unavailableQuality(reason),
		Cost:    CostSection{Usage: unavailableUsage(reason), BySeat: []Breakdown{}, ByModel: []Breakdown{}, Stories: []StoryCost{}},
		Spread:  []Spread{},
	}
	ratio := Ratio{Reason: reason}
	for _, unit := range costview.EstimateUnits() {
		r.Spread = append(r.Spread, Spread{Unit: unit, Stories: none, Unavailable: none, Median: ratio, P90: ratio, Min: ratio, Max: ratio})
	}
	for _, k := range dayKeys(w) {
		r.Days = append(r.Days, Bucket{Key: k, Stories: none, Epics: none})
	}
	for _, k := range weekKeys(w) {
		r.Weeks = append(r.Weeks, Bucket{Key: k, Stories: none, Epics: none})
	}
	return r
}

func dayKey(t time.Time) string { return t.UTC().Format(time.DateOnly) }

func weekKey(t time.Time) string {
	y, wk := t.UTC().ISOWeek()
	return fmt.Sprintf("%04d-W%02d", y, wk)
}

// dayKeys is every UTC day the window touches, oldest first.
func dayKeys(w Window) []string { return keys(w, dayKey) }

// weekKeys is every ISO week the window touches, oldest first.
func weekKeys(w Window) []string { return keys(w, weekKey) }

// keys walks the window a day at a time, keeping each new key in order.
func keys(w Window, key func(time.Time) string) []string {
	if w.Until.Before(w.Since) {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(t time.Time) {
		if k := key(t); !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for t := w.Since; t.Before(w.Until); t = t.Add(24 * time.Hour) {
		add(t)
	}
	add(w.Until)
	return out
}
