package throughput_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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

	r := throughput.Summarise("r", "", []throughput.Item{{Finished: p.End, Phases: p}}, window())
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
	}, window())
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
	r := throughput.Summarise("r", "/r", items, w)
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
	r := throughput.Summarise("r", "", nil, window())
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
}
