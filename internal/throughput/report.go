package throughput

import (
	"fmt"
	"sort"
	"time"
)

// The reasons a figure is unavailable. Each is said in place of the figure.
const (
	ReasonNoFinished  = "no finished items in window"
	ReasonNotRecorded = "not recorded"
	ReasonNoPRStep    = "no step on the route is marked pr_step"
	ReasonNoRepo      = "no readable repo"
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

// RepoReport is one repo's figures, or the total's.
type RepoReport struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
	// Error is why the repo could not be read; every figure is then unavailable
	// and the repo is left out of the total.
	Error string `json:"error,omitempty"`
	// Excluded names the repos left out of the total because they could not be
	// read. Set on the total only.
	Excluded []string `json:"excluded,omitempty"`
	Stories  Count    `json:"stories"`
	Epics    Count    `json:"epics"`
	Days     []Bucket `json:"days"`
	Weeks    []Bucket `json:"weeks"`
	Phase1   Phase    `json:"phase1"`
	Phase2   Phase    `json:"phase2"`
	Steps    Steps    `json:"steps"`
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
// repo, for the total) over w.
func Summarise(name, path string, items []Item, w Window) RepoReport {
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
	return r
}

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
		Steps: Steps{Rows: []Step{}, Reason: reason},
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
