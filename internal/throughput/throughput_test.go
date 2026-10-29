package throughput_test

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/throughput"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const prDone = "[feature]\nobligations = [\"raised\", \"coded\", \"pr-raised\", \"closed\"]\n"

func prSteps(marker string) string {
	return `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[pr-raised]
status = "pr_raising"
agent = "executor"
requires = ["coded"]
` + marker + `
[closed]
status = "done"
terminal = true
requires = ["pr-raised"]
`
}

func route(t *testing.T, marker string) wfdot.Spec {
	t.Helper()
	spec, err := wfdot.ParseRoute(prDone, prSteps(marker), "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	return spec
}

var t0 = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

func created(at time.Time) ledger.Entry {
	return ledger.Entry{ID: "evt_c", Kind: ledger.KindStoryCreated, Payload: json.RawMessage(`{"status":"backlog"}`), CreatedAt: at}
}

func moved(n int, at time.Time, from, to string) ledger.Entry {
	p, _ := json.Marshal(map[string]string{"from": from, "to": to})
	return ledger.Entry{ID: fmt.Sprintf("evt_%02d", n), Kind: ledger.KindStatusTransition, Payload: p, CreatedAt: at}
}

func hours(h float64) time.Time { return t0.Add(time.Duration(h * float64(time.Hour))) }

func TestCycleSplitsAtTheFirstEntryToThePRStep(t *testing.T) {
	spec := route(t, "pr_step = true\n")
	// Delivered newest-first: Cycle orders the rows itself.
	rows := []ledger.Entry{
		moved(5, hours(9), "pr_raising", "done"),
		moved(4, hours(5), "in_progress", "pr_raising"),
		moved(3, hours(4), "pr_raising", "in_progress"),
		moved(2, hours(3), "in_progress", "pr_raising"),
		moved(1, hours(1), "backlog", "in_progress"),
		created(t0),
	}
	p, ok := throughput.Cycle(rows, spec)
	if !ok || !p.Phase2Applicable {
		t.Fatalf("Cycle = %+v, %v; want a finished item on a PR route", p, ok)
	}
	if d, ok := p.Phase1(); !ok || d != 3*time.Hour {
		t.Errorf("phase 1 = %v, %v; want 3h (backlog to the first pr_raising)", d, ok)
	}
	if d, ok := p.Phase2(); !ok || d != 6*time.Hour {
		t.Errorf("phase 2 = %v, %v; want 6h (first pr_raising to done)", d, ok)
	}
}

func TestCycleWithNoPRStepIsAllPhaseOne(t *testing.T) {
	spec := route(t, "")
	rows := []ledger.Entry{
		created(t0),
		moved(1, hours(1), "backlog", "in_progress"),
		moved(2, hours(2), "in_progress", "pr_raising"),
		moved(3, hours(4), "pr_raising", "done"),
	}
	p, ok := throughput.Cycle(rows, spec)
	if !ok || p.Phase2Applicable {
		t.Fatalf("Cycle = %+v, %v; want finished, phase 2 not applicable", p, ok)
	}
	if d, ok := p.Phase1(); !ok || d != 4*time.Hour {
		t.Errorf("phase 1 = %v, %v; want the whole 4h", d, ok)
	}
	if _, ok := p.Phase2(); ok {
		t.Error("phase 2 recorded on a route with no pr_step")
	}

	r := throughput.Summarise("r", "", []throughput.Item{{Finished: p.End, Phases: p}}, window(), throughput.Returns{})
	if !r.Phase2.Median.NotApplicable || throughput.DurationText(r.Phase2.Median) != "n/a" {
		t.Errorf("phase 2 median = %+v, want n/a", r.Phase2.Median)
	}
}

func TestCycleIsNotFinishedOffATerminalState(t *testing.T) {
	rows := []ledger.Entry{created(t0), moved(1, hours(1), "backlog", "in_progress")}
	if _, ok := throughput.Cycle(rows, route(t, "")); ok {
		t.Error("an item last moved to in_progress must not count as finished")
	}
	// No resolved route: the store's backlog and done stand in.
	done := append(rows, moved(2, hours(3), "in_progress", workitem.StatusDone))
	p, ok := throughput.Cycle(done, wfdot.Spec{})
	if d, recorded := p.Phase1(); !ok || !recorded || d != 3*time.Hour || p.Phase2Applicable {
		t.Errorf("zero-Spec cycle = %+v, %v; want 3h phase 1 and no phase 2", p, ok)
	}
}

func TestMissingStartAndBoundaryAreNotRecorded(t *testing.T) {
	spec := route(t, "pr_step = true\n")
	// No story_created row and no entry to backlog: the start is not recorded.
	noStart, ok := throughput.Cycle([]ledger.Entry{
		moved(1, hours(1), "backlog", "in_progress"),
		moved(2, hours(2), "in_progress", "pr_raising"),
		moved(3, hours(5), "pr_raising", "done"),
	}, spec)
	if !ok {
		t.Fatal("want finished")
	}
	if _, rec := noStart.Phase1(); rec {
		t.Error("phase 1 recorded with no start row")
	}
	// Never entered the PR step on a route that has one.
	noBoundary, ok := throughput.Cycle([]ledger.Entry{
		created(t0),
		moved(1, hours(1), "backlog", "done"),
	}, spec)
	if !ok {
		t.Fatal("want finished")
	}
	if _, rec := noBoundary.Phase2(); rec {
		t.Error("phase 2 recorded with no entry to the PR step")
	}

	r := throughput.Summarise("r", "", []throughput.Item{
		{Finished: noStart.End, Phases: noStart},
		{Finished: noBoundary.End, Phases: noBoundary},
	}, window(), throughput.Returns{})
	if r.Phase1.Median.Seconds != nil || r.Phase1.Median.Reason != throughput.ReasonNotRecorded {
		t.Errorf("phase 1 median = %+v, want unavailable: not recorded", r.Phase1.Median)
	}
	if got := throughput.DurationText(r.Phase1.P90); got != "unavailable (not recorded)" {
		t.Errorf("phase 1 p90 text = %q", got)
	}
	// Phase 2 has one recorded item (noStart), so it is a figure, not n/a.
	if r.Phase2.Items != 1 || r.Phase2.Median.Seconds == nil || *r.Phase2.Median.Seconds != 3*3600 {
		t.Errorf("phase 2 = %+v, want 1 item at 3h", r.Phase2)
	}
}

func TestPercentileIsNearestRank(t *testing.T) {
	var ds []time.Duration
	for i := 10; i >= 1; i-- {
		ds = append(ds, time.Duration(i)*time.Second)
	}
	if got := throughput.Median(ds); got != 5*time.Second {
		t.Errorf("median of 1..10s = %v, want 5s (lower middle)", got)
	}
	if got := throughput.P90(ds); got != 9*time.Second {
		t.Errorf("p90 of 1..10s = %v, want 9s", got)
	}
	if ds[0] != 10*time.Second {
		t.Error("Percentile reordered its input")
	}
	one := []time.Duration{4 * time.Minute}
	if throughput.Median(one) != 4*time.Minute || throughput.P90(one) != 4*time.Minute {
		t.Error("a population of one is its own median and p90")
	}
}

func window() throughput.Window {
	return throughput.Window{Since: t0.Add(-24 * time.Hour), Until: t0.Add(48 * time.Hour)}
}

func TestSummariseCountsBucketsAndStepMedians(t *testing.T) {
	w := window()
	items := []throughput.Item{
		{ID: "a", Finished: hours(1), Dwell: map[string]time.Duration{"backlog": time.Hour, "in_progress": 2 * time.Hour}},
		{ID: "b", Finished: hours(2), Dwell: map[string]time.Duration{"backlog": 3 * time.Hour, "in_progress": 4 * time.Hour}},
		{ID: "c", Finished: hours(30), Dwell: map[string]time.Duration{"backlog": 5 * time.Hour, "blocked": 7 * time.Hour}},
		{ID: "e", Epic: true, Finished: hours(30), Dwell: map[string]time.Duration{"ready": 9 * time.Hour}},
	}
	r := throughput.Summarise("r", "/r", items, w, throughput.Returns{})
	if *r.Stories.Value != 3 || *r.Epics.Value != 1 {
		t.Errorf("stories, epics = %d, %d; want 3, 1", *r.Stories.Value, *r.Epics.Value)
	}
	days := map[string][2]int{}
	for _, b := range r.Days {
		days[b.Key] = [2]int{*b.Stories.Value, *b.Epics.Value}
	}
	wantDays := map[string][2]int{"2026-10-19": {0, 0}, "2026-10-20": {2, 0}, "2026-10-21": {1, 1}, "2026-10-22": {0, 0}}
	if fmt.Sprint(days) != fmt.Sprint(wantDays) {
		t.Errorf("days = %v, want %v", days, wantDays)
	}
	if len(r.Weeks) != 1 || r.Weeks[0].Key != "2026-W43" || *r.Weeks[0].Stories.Value != 3 || *r.Weeks[0].Epics.Value != 1 {
		t.Errorf("weeks = %+v, want one W43 bucket of 3 stories and 1 epic", r.Weeks)
	}
	medians := map[string]string{}
	for _, s := range r.Steps.Rows {
		medians[s.Status] = fmt.Sprintf("%s/%d", throughput.DurationText(s.Median), s.Items)
	}
	want := map[string]string{"backlog": "3h0m0s/3", "in_progress": "2h0m0s/2", "blocked": "7h0m0s/1", "ready": "9h0m0s/1"}
	if fmt.Sprint(medians) != fmt.Sprint(want) {
		t.Errorf("step medians = %v, want %v", medians, want)
	}
}

func TestAnEmptyWindowCountsZeroAndTimesNothing(t *testing.T) {
	r := throughput.Summarise("r", "", nil, window(), throughput.Returns{})
	if r.Stories.Value == nil || *r.Stories.Value != 0 || throughput.CountText(r.Epics) != "0" {
		t.Errorf("counts = %+v / %+v, want a real 0", r.Stories, r.Epics)
	}
	for _, b := range r.Days {
		if b.Stories.Value == nil || *b.Stories.Value != 0 {
			t.Errorf("day %s = %+v, want a real 0", b.Key, b.Stories)
		}
	}
	for _, d := range []throughput.Duration{r.Phase1.Median, r.Phase1.P90, r.Phase2.Median, r.Phase2.P90} {
		if d.Seconds != nil || d.Reason != throughput.ReasonNoFinished {
			t.Errorf("figure = %+v, want unavailable: %s", d, throughput.ReasonNoFinished)
		}
	}
	if len(r.Steps.Rows) != 0 || r.Steps.Reason != throughput.ReasonNoFinished {
		t.Errorf("steps = %+v, want none, unavailable", r.Steps)
	}
}

func TestAnUnreadableRepoIsUnavailableNotZero(t *testing.T) {
	r := throughput.Unavailable("gone", "/gone", "repo unreadable: boom", window())
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"stories":{"value":null,"reason":"repo unreadable: boom"}`, `"median":{"seconds":null,"reason":"repo unreadable: boom"}`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("JSON missing %s:\n%s", want, raw)
		}
	}
	if got := throughput.CountText(r.Stories); got != "unavailable (repo unreadable: boom)" {
		t.Errorf("count text = %q", got)
	}
	for _, b := range r.Days {
		if b.Stories.Value != nil {
			t.Errorf("day %s of an unreadable repo = %d, want unavailable", b.Key, *b.Stories.Value)
		}
	}

	// Its cost and estimate figures are unavailable for the same reason.
	if !strings.Contains(string(raw), `"cost":{"usd":null,"reason":"repo unreadable: boom"`) {
		t.Errorf("JSON cost is not null with the repo's reason:\n%s", raw)
	}
	if u := r.Cost.Usage; u.FreshInput.Value != nil || u.Output.Value != nil || u.Cost.USD != nil || len(r.Cost.Stories) != 0 {
		t.Errorf("cost of an unreadable repo = %+v, want unavailable and no items", r.Cost)
	}
	if len(r.Spread) != len(costview.EstimateUnits()) {
		t.Fatalf("spread = %+v, want one row per estimate unit", r.Spread)
	}
	for _, s := range r.Spread {
		if s.Stories.Value != nil || s.Unavailable.Value != nil || s.Median.Value != nil || s.Median.Reason != "repo unreadable: boom" {
			t.Errorf("spread %s of an unreadable repo = %+v, want unavailable", s.Unit, s)
		}
	}
	var text strings.Builder
	throughput.Text(&text, throughput.Report{Window: window(), Repos: []throughput.RepoReport{r}, Total: r})
	if !strings.Contains(text.String(), "cost unavailable (repo unreadable: boom)") {
		t.Errorf("text cost of an unreadable repo is not unavailable:\n%s", text.String())
	}
}

// Hand-priced fixtures, USD per million tokens. m-free has no rate.
var pricing = costview.Pricing{Models: map[string]costview.Rates{
	"m-big":   {FreshInput: 3, CacheRead: 0.3, CacheWrite: 3.75, Output: 15},
	"m-small": {FreshInput: 1, CacheRead: 0.1, CacheWrite: 1.25, Output: 5},
}}

var engageToDone = costview.Clock{
	Engaging: func(s string) bool { return s == "in_progress" },
	Terminal: func(s string) bool { return s == workitem.StatusDone },
}

// usage is a measured dispatch row of agent on model.
func usage(agent, model string, fresh, read, write, out int, extra ...string) ledger.Entry {
	p := map[string]any{
		"agent": agent, "model_resolved": model, "adapter": "claude", "usage_available": true,
		"tokens_in_fresh": fresh, "tokens_cache_read": read, "tokens_cache_write": write,
		"tokens_in": fresh + read + write, "tokens_out": out,
	}
	for i := 0; i+1 < len(extra); i += 2 {
		p[extra[i]] = extra[i+1]
	}
	raw, _ := json.Marshal(p)
	return ledger.Entry{ID: "evt_u", Kind: ledger.KindAgentInvocation, Payload: raw, CreatedAt: hours(2)}
}

// unreported is a dispatch row whose seat reported no usage, as a pi command
// seat before pi-rpc does.
func unreported(agent, model string) ledger.Entry {
	raw, _ := json.Marshal(map[string]any{"agent": agent, "model": model, "adapter": "pi", "usage_available": false})
	return ledger.Entry{ID: "evt_n", Kind: ledger.KindAgentInvocation, Payload: raw, CreatedAt: hours(2)}
}

// finished is an item engaged at hour 1 and done at doneAt, carrying tags and
// the extra rows, with its own cost folded as ReadRepo folds it.
func finished(t *testing.T, id string, doneAt time.Time, tags []string, extra ...ledger.Entry) throughput.Item {
	t.Helper()
	rows := append([]ledger.Entry{
		created(t0),
		moved(1, hours(1), "backlog", "in_progress"),
		moved(2, doneAt, "in_progress", workitem.StatusDone),
	}, extra...)
	it := workitem.Item{ID: id, Title: "title " + id, Tags: tags}
	fi, ok := throughput.Finish(it, rows, wfdot.Spec{})
	if !ok {
		t.Fatalf("%s did not finish", id)
	}
	fi.Own = costview.Own(it, rows, engageToDone, doneAt, costview.WithPricing(pricing))
	return fi
}

func value(t *testing.T, what string, c throughput.Count) int {
	t.Helper()
	if c.Value == nil {
		t.Fatalf("%s unavailable (%s), want a figure", what, c.Reason)
	}
	return *c.Value
}

func usd(t *testing.T, what string, c throughput.Cost) float64 {
	t.Helper()
	if c.USD == nil {
		t.Fatalf("%s cost unavailable (%s), want a figure", what, c.Reason)
	}
	return *c.USD
}

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func named(t *testing.T, bs []throughput.Breakdown, name string) throughput.Breakdown {
	t.Helper()
	for _, b := range bs {
		if b.Name == name {
			return b
		}
	}
	t.Fatalf("no breakdown %q in %+v", name, bs)
	return throughput.Breakdown{}
}

// AC1/AC2: each finished item's own tokens and cost, and its seat and model
// breakdowns, whose sums equal the item's total; a bundled reviewer session
// counts once.
func TestCostPerItemBySeatAndModel(t *testing.T) {
	it := finished(t, "sty_a", hours(3), nil,
		usage("coder", "m-big", 1_000_000, 2_000_000, 0, 100_000),               // $3 + $0.60 + $1.50
		usage("coder", "m-small", 100_000, 0, 100_000, 10_000),                  // $0.10 + $0.125 + $0.05
		usage("reviewer", "m-small", 500_000, 0, 0, 200_000, "bundle_id", "b1"), // $0.50 + $1.00
	)
	r := throughput.Summarise("r", "/r", []throughput.Item{it}, window(), throughput.Returns{})
	if len(r.Cost.Stories) != 1 {
		t.Fatalf("cost items = %+v, want one", r.Cost.Stories)
	}
	s := r.Cost.Stories[0]
	if s.ID != "sty_a" || s.Title != "title sty_a" {
		t.Errorf("item = %s %q", s.ID, s.Title)
	}
	want := map[string]int{"fresh input": 1_600_000, "cache read": 2_000_000, "cache write": 100_000, "output": 310_000}
	got := map[string]int{
		"fresh input": value(t, "fresh input", s.FreshInput), "cache read": value(t, "cache read", s.CacheRead),
		"cache write": value(t, "cache write", s.CacheWrite), "output": value(t, "output", s.Output),
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("item tokens = %v, want %v", got, want)
	}
	near(t, "item cost", usd(t, "item", s.Cost), 6.875)

	for label, bs := range map[string][]throughput.Breakdown{"seat": s.BySeat, "model": s.ByModel} {
		var fresh, read, write, out int
		var cost float64
		for _, b := range bs {
			fresh += value(t, b.Name, b.FreshInput)
			read += value(t, b.Name, b.CacheRead)
			write += value(t, b.Name, b.CacheWrite)
			out += value(t, b.Name, b.Output)
			cost += usd(t, b.Name, b.Cost)
		}
		if fresh != want["fresh input"] || read != want["cache read"] || write != want["cache write"] || out != want["output"] {
			t.Errorf("by %s sums = %d/%d/%d/%d, want the item total", label, fresh, read, write, out)
		}
		near(t, "by "+label+" cost", cost, 6.875)
	}
	if len(s.BySeat) != 2 || s.BySeat[0].Name != "coder" {
		t.Errorf("by seat = %+v, want coder then reviewer, highest cost first", s.BySeat)
	}
	near(t, "coder", usd(t, "coder", named(t, s.BySeat, "coder").Cost), 5.375)
	if rev := named(t, s.BySeat, "reviewer"); value(t, "reviewer", rev.FreshInput) != 500_000 {
		t.Errorf("bundled reviewer fresh input = %+v, want counted once", rev.FreshInput)
	}
	near(t, "m-big", usd(t, "m-big", named(t, s.ByModel, "m-big").Cost), 5.1)
	near(t, "m-small", usd(t, "m-small", named(t, s.ByModel, "m-small").Cost), 1.775)

	// The repo section sums its items the same way.
	near(t, "repo cost", usd(t, "repo", r.Cost.Cost), 6.875)
	near(t, "repo coder", usd(t, "repo coder", named(t, r.Cost.BySeat, "coder").Cost), 5.375)
	near(t, "repo m-small", usd(t, "repo m-small", named(t, r.Cost.ByModel, "m-small").Cost), 1.775)
}

// AC3: a seat that reported no usage and a model with no rate read unavailable
// in JSON (null with a reason) and in text, never 0.
func TestUnmeasuredSeatAndUnpricedModelAreUnavailable(t *testing.T) {
	it := finished(t, "sty_b", hours(3), nil,
		unreported("pi-coder", "m-pi"),
		usage("reviewer", "m-free", 1000, 0, 0, 100),
	)
	r := throughput.Summarise("r", "/r", []throughput.Item{it}, window(), throughput.Returns{})
	s := r.Cost.Stories[0]

	pi := named(t, s.BySeat, "pi-coder")
	for _, c := range []throughput.Count{pi.FreshInput, pi.CacheRead, pi.CacheWrite, pi.Output} {
		if c.Value != nil || !strings.Contains(c.Reason, throughput.ReasonNotRecorded) {
			t.Errorf("unmeasured seat figure = %+v, want null: not recorded", c)
		}
	}
	if pi.Cost.USD != nil || pi.Cost.Reason == "" {
		t.Errorf("unmeasured seat cost = %+v, want null with a reason", pi.Cost)
	}
	if m := named(t, s.ByModel, "m-pi"); m.Output.Value != nil {
		t.Errorf("unmeasured model output = %d, want unavailable", *m.Output.Value)
	}

	free := named(t, s.ByModel, "m-free")
	if value(t, "m-free output", free.Output) != 100 {
		t.Errorf("m-free output = %+v, want the measured 100", free.Output)
	}
	if free.Cost.USD != nil || !strings.Contains(free.Cost.Reason, "m-free has no rate") {
		t.Errorf("unpriced model cost = %+v, want null: no rate", free.Cost)
	}

	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"name":"pi-coder","fresh_input":{"value":null,"reason":"not recorded`) {
		t.Errorf("JSON of the unmeasured seat is not null with a reason:\n%s", raw)
	}
	var text strings.Builder
	throughput.Text(&text, throughput.Report{Window: window(), Repos: []throughput.RepoReport{r}, Total: r})
	for _, line := range strings.Split(text.String(), "\n") {
		if strings.Contains(line, "by seat pi-coder") {
			if !strings.Contains(line, "fresh input unavailable") || !strings.Contains(line, "cost unavailable") || strings.Contains(line, " 0 ") {
				t.Errorf("text of the unmeasured seat = %q, want unavailable figures", line)
			}
		}
		if strings.Contains(line, "by model m-free") && !strings.Contains(line, "cost unavailable (model m-free has no rate)") {
			t.Errorf("text of the unpriced model = %q", line)
		}
	}
}

func estimate(t *testing.T, s throughput.StoryCost, unit string) throughput.EstimateVsActual {
	t.Helper()
	for _, e := range s.Estimates {
		if e.Unit == unit {
			return e
		}
	}
	t.Fatalf("%s has no %s estimate in %+v", s.ID, unit, s.Estimates)
	return throughput.EstimateVsActual{}
}

// AC4: an estimated item shows estimate, actual and actual÷estimate per unit; an
// unestimated item and an unmeasured actual read unavailable.
func TestEstimateAgainstActualPerItem(t *testing.T) {
	// Engaged at hour 1, done 2m30s later: recordActual writes 2 whole minutes.
	estimated := finished(t, "sty_e", hours(1).Add(2*time.Minute+30*time.Second),
		[]string{"estimate-fresh-input:800", "estimate-output:400", "estimate-tokens:2000", "estimate-minutes:4", "estimate-usd:9"},
		usage("coder", "m-big", 1000, 5000, 200, 100))
	unestimated := finished(t, "sty_u", hours(3), nil, usage("coder", "m-big", 10, 0, 0, 10))
	unmeasured := finished(t, "sty_n", hours(3), []string{"estimate-output:400"}, unreported("pi-coder", "m-pi"))
	r := throughput.Summarise("r", "", []throughput.Item{estimated, unestimated, unmeasured}, window(), throughput.Returns{})
	by := map[string]throughput.StoryCost{}
	for _, s := range r.Cost.Stories {
		by[s.ID] = s
	}

	e := by["sty_e"]
	if len(e.Estimates) != 4 {
		t.Errorf("estimates = %+v, want fresh-input, output, minutes and tokens (usd is not an estimate)", e.Estimates)
	}
	for unit, w := range map[string]struct {
		actual int
		ratio  float64
	}{
		costview.UnitFreshInput: {1000, 1.25},
		costview.UnitOutput:     {100, 0.25},
		costview.UnitTokens:     {1300, 0.65}, // fresh + output + cache write, cache read excluded
		costview.UnitMinutes:    {2, 0.5},
	} {
		ev := estimate(t, e, unit)
		if ev.Actual == nil || *ev.Actual != w.actual || ev.Ratio == nil {
			t.Errorf("%s = %+v, want actual %d and a ratio", unit, ev, w.actual)
			continue
		}
		near(t, unit+" ratio", *ev.Ratio, w.ratio)
	}

	if u := by["sty_u"]; len(u.Estimates) != 0 || u.EstimatesReason != throughput.ReasonNoEstimate {
		t.Errorf("unestimated = %+v / %q, want none: no estimate", u.Estimates, u.EstimatesReason)
	}
	if n := estimate(t, by["sty_n"], costview.UnitOutput); n.Actual != nil || n.Ratio != nil || n.Reason != throughput.ReasonNotRecorded {
		t.Errorf("unmeasured = %+v, want actual and ratio unavailable: not recorded", n)
	}

	var text strings.Builder
	throughput.Text(&text, throughput.Report{Window: window(), Repos: []throughput.RepoReport{r}, Total: r})
	for _, want := range []string{
		"estimate minutes      est 4  actual 2  actual÷estimate 0.50",
		"estimate: unavailable (no estimate)",
		"estimate output       est 400  actual unavailable (not recorded)  actual÷estimate unavailable (not recorded)",
	} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text missing %q:\n%s", want, text.String())
		}
	}
}

// AC5: the nearest-rank spread of actual÷estimate over the items with both
// figures, with the items left out counted.
func TestEstimateSpreadIsNearestRank(t *testing.T) {
	var items []throughput.Item
	for i, out := range []int{4000, 500, 2000, 1000} {
		items = append(items, finished(t, fmt.Sprintf("sty_%d", i), hours(3), []string{"estimate-output:1000"}, usage("coder", "m-big", 1, 0, 0, out)))
	}
	items = append(items,
		finished(t, "sty_u", hours(3), nil, usage("coder", "m-big", 1, 0, 0, 1)),
		finished(t, "sty_n", hours(3), []string{"estimate-output:1000"}, unreported("pi-coder", "m-pi")),
	)
	r := throughput.Summarise("r", "", items, window(), throughput.Returns{})
	var out throughput.Spread
	for _, s := range r.Spread {
		if s.Unit == costview.UnitOutput {
			out = s
		}
	}
	if value(t, "items", out.Stories) != 4 || value(t, "unavailable", out.Unavailable) != 2 {
		t.Errorf("output spread counts = %+v / %+v, want 4 and 2", out.Stories, out.Unavailable)
	}
	for what, w := range map[string]struct {
		r    throughput.Ratio
		want float64
	}{
		"median": {out.Median, 1.0}, // lower middle of 0.5, 1, 2, 4
		"p90":    {out.P90, 4.0},
		"min":    {out.Min, 0.5},
		"max":    {out.Max, 4.0},
	} {
		if w.r.Value == nil {
			t.Errorf("%s unavailable (%s)", what, w.r.Reason)
			continue
		}
		near(t, what, *w.r.Value, w.want)
	}
	for _, s := range r.Spread {
		if s.Unit == costview.UnitFreshInput && (s.Median.Value != nil || s.Median.Reason != throughput.ReasonNoRatio || value(t, "fresh unavailable", s.Unavailable) != 6) {
			t.Errorf("fresh-input spread = %+v, want no ratio and all 6 left out", s)
		}
	}
}
