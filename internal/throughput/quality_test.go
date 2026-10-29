package throughput_test

import (
	"bytes"
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

// planRoute is a route with readiness, plan and integration steps that marks no
// pr_step and no freeze.
func planRoute(t *testing.T) wfdot.Spec {
	t.Helper()
	spec, err := wfdot.ParseRoute("[feature]\nobligations = [\"raised\", \"readied\", \"coded\", \"integrated\", \"closed\"]\n", `[raised]
status = "backlog"
start = true

[readied]
status = "plan"
agent = "executor"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["readied"]

[integrated]
status = "integration"
agent = "executor"
requires = ["coded"]

[closed]
status = "done"
terminal = true
requires = ["integrated"]
`, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	if spec.HasPRState() {
		t.Fatal("fixture route must mark no pr_step")
	}
	return spec
}

var rowN int

func verdict(kind, from, to, attempt, skill string, extra map[string]any) ledger.Entry {
	rowN++
	p := map[string]any{"from": from, "to": to, "attempt": attempt, "skill": skill}
	for k, v := range extra {
		p[k] = v
	}
	b, _ := json.Marshal(p)
	return ledger.Entry{ID: fmt.Sprintf("evt_q%04d", rowN), Kind: kind, Payload: b, CreatedAt: t0.Add(time.Duration(rowN) * time.Second)}
}

func acc(from, to, attempt, skill string) ledger.Entry {
	return verdict(ledger.KindReviewAccept, from, to, attempt, skill, nil)
}

func rej(from, to, attempt, skill string) ledger.Entry {
	return verdict(ledger.KindReviewReject, from, to, attempt, skill, nil)
}

func invoked(agent, from, to string) ledger.Entry {
	return verdict(ledger.KindAgentInvocation, from, to, "", "", map[string]any{"agent": agent})
}

func item(id string, spec wfdot.Spec, rows ...ledger.Entry) throughput.Item {
	return throughput.Item{ID: id, Finished: hours(1), Spec: spec, Rows: rows}
}

func qualityText(r throughput.RepoReport) string {
	var b bytes.Buffer
	throughput.Text(&b, throughput.Report{Window: window(), Total: r})
	return b.String()
}

func passes(tb throughput.PassTable) string {
	var out []string
	for _, p := range tb.Rows {
		out = append(out, fmt.Sprintf("%s %d/%d acc%v rej%v", p.Name, p.Accepted, p.Presented, p.AcceptedStories, p.RejectedStories))
	}
	return strings.Join(out, "; ")
}

// AC1: a step and a gate are accepted first time by their first presentation
// with a verdict; later accepts do not count.
func TestQualityFirstPassPerStepAndGate(t *testing.T) {
	spec := planRoute(t)
	items := []throughput.Item{
		item("sty_aaaaaaaa", spec,
			acc("backlog", "plan", "att_1", "intent"), acc("backlog", "plan", "att_1", "plan-review"),
			rej("plan", "in_progress", "att_2", "definition"),
			acc("plan", "in_progress", "att_3", "definition")),
		item("sty_bbbbbbbb", spec,
			rej("backlog", "plan", "att_4", "plan-review"), acc("backlog", "plan", "att_4", "intent"),
			acc("backlog", "plan", "att_5", "plan-review"), acc("backlog", "plan", "att_5", "intent")),
	}
	q := throughput.Quality(items, throughput.Returns{})
	wantSteps := "plan 1/2 acc[sty_aaaaaaaa] rej[sty_bbbbbbbb]; in_progress 0/1 acc[] rej[sty_aaaaaaaa]"
	if got := passes(q.FirstPassByStep); got != wantSteps {
		t.Errorf("by step = %s\nwant      %s", got, wantSteps)
	}
	wantGates := "definition 0/1 acc[] rej[sty_aaaaaaaa]; intent 2/2 acc[sty_aaaaaaaa sty_bbbbbbbb] rej[]; plan-review 1/2 acc[sty_aaaaaaaa] rej[sty_bbbbbbbb]"
	if got := passes(q.FirstPassByGate); got != wantGates {
		t.Errorf("by gate = %s\nwant      %s", got, wantGates)
	}
	if v := q.FirstPassByStep.Rows[0].Share.Value; v == nil || *v != 0.5 {
		t.Errorf("plan share = %v, want 0.5", v)
	}
	text := qualityText(throughput.Summarise("total", "", items, window(), throughput.Returns{}))
	for _, want := range []string{
		"first-pass acceptance by step:",
		"plan                                 1/2 (50%)  accepted: sty_aaaaaaaa  rejected: sty_bbbbbbbb",
		"plan-review                          1/2 (50%)  accepted: sty_aaaaaaaa  rejected: sty_bbbbbbbb",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}
}

// AC2 and AC7: rejected rounds per story on every gated edge, in route order,
// on a route with no pr_step or freeze; the recorded integration rejections show.
func TestQualityRoundsPerGatedEdge(t *testing.T) {
	spec := planRoute(t)
	items := []throughput.Item{
		item("sty_00000000", spec,
			acc("plan", "in_progress", "att_a", "definition"),
			acc("in_progress", "integration", "att_b", "scope")),
		item("sty_11111111", spec,
			rej("backlog", "plan", "att_c", "plan-review"), acc("backlog", "plan", "att_d", "plan-review"),
			rej("plan", "in_progress", "att_e", "definition"), acc("plan", "in_progress", "att_f", "definition")),
		item("sty_33333333", spec,
			rej("plan", "in_progress", "att_g", "definition"), rej("plan", "in_progress", "att_h", "definition"),
			rej("plan", "in_progress", "att_i", "definition"), acc("plan", "in_progress", "att_j", "definition"),
			rej("in_progress", "integration", "att_k", "scope"), rej("in_progress", "integration", "att_l", "build"),
			acc("in_progress", "integration", "att_m", "scope")),
	}
	q := throughput.Quality(items, throughput.Returns{})
	var got []string
	for _, r := range q.Rounds.Rows {
		got = append(got, fmt.Sprintf("%s>%s n=%d mean=%.4f max=%d at%v rej%v", r.From, r.To, r.Stories, *r.Mean.Value, *r.Max.Value, r.AtMax, r.Rejected))
	}
	want := []string{
		"backlog>plan n=1 mean=1.0000 max=1 at[sty_11111111] rej[sty_11111111]",
		"plan>in_progress n=3 mean=1.3333 max=3 at[sty_33333333] rej[sty_11111111 sty_33333333]",
		"in_progress>integration n=2 mean=1.0000 max=2 at[sty_33333333] rej[sty_33333333]",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("rounds =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	text := qualityText(throughput.Summarise("total", "", items, window(), throughput.Returns{}))
	if !strings.Contains(text, "plan → in_progress           mean 1.3  max 3  (3 stories)  at max: sty_33333333  rejected: sty_11111111, sty_33333333") {
		t.Errorf("text missing the plan rounds row:\n%s", text)
	}
	if !strings.Contains(text, "in_progress → integration    mean 1.0  max 2") {
		t.Errorf("recorded integration rejections must show on a route with no pr_step:\n%s", text)
	}
}

// AC3: a performer re-run at a step counts only after an exit of it was rejected.
func TestQualityRedispatchesAfterAnExitReject(t *testing.T) {
	spec := planRoute(t)
	items := []throughput.Item{
		item("sty_cccccccc", spec,
			invoked("coder", "plan", "in_progress"),
			invoked("coder", "in_progress", "in_progress"), // before any reject
			rej("in_progress", "integration", "att_1", "scope"),
			invoked("coder", "in_progress", "in_progress"), // the re-dispatch
			acc("in_progress", "integration", "att_2", "scope")),
		item("sty_dddddddd", spec, acc("in_progress", "integration", "att_3", "scope")),
	}
	q := throughput.Quality(items, throughput.Returns{})
	if c := q.Redispatches.Count; c.Value == nil || *c.Value != 1 || fmt.Sprint(q.Redispatches.Stories) != "[sty_cccccccc]" {
		t.Errorf("redispatches = %+v, want 1 by sty_cccccccc", q.Redispatches)
	}
	text := qualityText(throughput.Summarise("total", "", items, window(), throughput.Returns{}))
	if !strings.Contains(text, "coder re-dispatches after an exit rejection: 1  stories: sty_cccccccc") {
		t.Errorf("text:\n%s", text)
	}

	// No exit was rejected: the figure has no rows behind it.
	none := throughput.Quality(items[1:], throughput.Returns{})
	if none.Redispatches.Count.Value != nil || none.Redispatches.Count.Reason != throughput.ReasonNoExitReject {
		t.Errorf("redispatches with no exit reject = %+v, want unavailable", none.Redispatches.Count)
	}
}

// AC4: a reject names a criterion only with an entry numbered 1 or higher that
// rejects or is unmet.
func TestQualityRejectsNamingNoCriterion(t *testing.T) {
	spec := planRoute(t)
	crit := func(n int, status string) map[string]any {
		return map[string]any{"criteria": []map[string]any{{"criterion": n, "status": status}}}
	}
	items := []throughput.Item{
		item("sty_eeeeeeee", spec, verdict(ledger.KindReviewReject, "plan", "in_progress", "att_1", "definition", crit(2, "reject"))),
		item("sty_ffffffff", spec,
			verdict(ledger.KindReviewReject, "plan", "in_progress", "att_2", "definition", crit(0, "reject")),
			rej("in_progress", "integration", "att_3", "scope")),
	}
	q := throughput.Quality(items, throughput.Returns{})
	nc := q.NoCriterion
	if nc.Count.Value == nil || *nc.Count.Value != 2 || nc.Rejects != 3 || fmt.Sprint(nc.Stories) != "[sty_ffffffff]" {
		t.Errorf("no-criterion = %+v, want 2 of 3 by sty_ffffffff", nc)
	}
	var by []string
	for _, s := range nc.BySkill {
		by = append(by, fmt.Sprintf("%s %d/%d %v", s.Skill, s.NoCriterion, s.Rejects, s.Stories))
	}
	if got := strings.Join(by, "; "); got != "definition 1/2 [sty_ffffffff]; scope 1/1 [sty_ffffffff]" {
		t.Errorf("by skill = %s", got)
	}
	text := qualityText(throughput.Summarise("total", "", items, window(), throughput.Returns{}))
	if !strings.Contains(text, "rejects naming no criterion: 2 of 3  stories: sty_ffffffff") {
		t.Errorf("text:\n%s", text)
	}

	// Rejects were recorded and every one named a criterion: a real zero.
	zero := throughput.Quality(items[:1], throughput.Returns{})
	if zero.NoCriterion.Count.Value == nil || *zero.NoCriterion.Count.Value != 0 {
		t.Errorf("every reject named a criterion: want a real 0, got %+v", zero.NoCriterion.Count)
	}
	// No reject at all: unavailable.
	noRejects := throughput.Quality([]throughput.Item{item("sty_99999999", spec, acc("plan", "in_progress", "att_9", "definition"))}, throughput.Returns{})
	if noRejects.NoCriterion.Count.Value != nil || noRejects.NoCriterion.Count.Reason != throughput.ReasonNoReject {
		t.Errorf("no rejects = %+v, want unavailable", noRejects.NoCriterion.Count)
	}
}

// AC5: a story created after the story it cites reached done came back; one
// created before, or citing a story that never reached done, did not.
func TestQualityWorkThatCameBack(t *testing.T) {
	ret := throughput.Returns{
		Created: []throughput.Created{
			{ID: "sty_10000000", At: hours(5), Cites: []string{"sty_d0000000"}},
			{ID: "sty_20000000", At: hours(1), Cites: []string{"sty_d0000000"}},
			{ID: "sty_30000000", At: hours(5), Cites: []string{"sty_c0000000"}},
		},
		Done: map[string]time.Time{"sty_d0000000": hours(2)},
	}
	q := throughput.Quality(nil, ret)
	cb := q.CameBack
	if cb.Count.Value == nil || *cb.Count.Value != 1 || len(cb.Pairs) != 1 || cb.Pairs[0] != (throughput.CameBackPair{Story: "sty_10000000", Cites: "sty_d0000000"}) {
		t.Errorf("came back = %+v, want sty_10000000 citing sty_d0000000", cb)
	}
	if fmt.Sprint(cb.Stories) != "[sty_10000000 sty_d0000000]" {
		t.Errorf("stories = %v", cb.Stories)
	}
	text := qualityText(throughput.Summarise("total", "", nil, window(), ret))
	if !strings.Contains(text, "work that came back: 1\n    sty_10000000 cites sty_d0000000\n") {
		t.Errorf("text:\n%s", text)
	}

	// A cancelled story never reaches a terminal state, so it has no done time.
	cancelled := []ledger.Entry{created(t0), moved(1, hours(1), "backlog", "in_progress"), moved(2, hours(2), "in_progress", workitem.StatusCancelled)}
	if _, ok := throughput.Cycle(cancelled, planRoute(t)); ok {
		t.Error("a cancelled story must not count as done")
	}
}

func TestCitesNamesOtherStoriesOnly(t *testing.T) {
	it := workitem.Item{
		ID: "sty_aaaaaaaa", ParentID: "sty_pppppppp",
		Title: "redo sty_bbbbbbbb", Body: "after sty_aaaaaaaa and (sty_cccccccc).",
		AcceptanceCriteria: "1. sty_bbbbbbbb holds", Tags: []string{"supersedes:sty_dddddddd", "epic:sty_pppppppp"},
	}
	if got := fmt.Sprint(throughput.Cites(it)); got != "[sty_bbbbbbbb sty_cccccccc sty_dddddddd]" {
		t.Errorf("Cites = %s", got)
	}
}

// AC7: with no rows behind them the figures read unavailable with a reason,
// never 0 — an empty window, no gated presentation, and an unreadable repo.
func TestQualityWithNoRowsIsUnavailable(t *testing.T) {
	check := func(name string, q throughput.QualityReport, reason, cameBack string) {
		t.Helper()
		for _, tb := range []throughput.PassTable{q.FirstPassByStep, q.FirstPassByGate} {
			if len(tb.Rows) != 0 || tb.Reason != reason {
				t.Errorf("%s: first pass = %+v, want unavailable: %s", name, tb, reason)
			}
		}
		if len(q.Rounds.Rows) != 0 || q.Rounds.Reason != reason {
			t.Errorf("%s: rounds = %+v, want unavailable: %s", name, q.Rounds, reason)
		}
		if q.CameBack.Count.Value != nil || q.CameBack.Count.Reason != cameBack {
			t.Errorf("%s: came back = %+v, want unavailable: %s", name, q.CameBack.Count, cameBack)
		}
	}
	empty := throughput.Summarise("r", "", nil, window(), throughput.Returns{})
	check("empty window", empty.Quality, throughput.ReasonNoFinished, throughput.ReasonNoCreated)
	for _, c := range []throughput.Count{empty.Quality.Redispatches.Count, empty.Quality.NoCriterion.Count} {
		if c.Value != nil || c.Reason != throughput.ReasonNoFinished {
			t.Errorf("empty window count = %+v, want unavailable", c)
		}
	}
	text := qualityText(empty)
	for _, want := range []string{
		"first-pass acceptance by step:\n    unavailable (no finished items in window)",
		"coder re-dispatches after an exit rejection: unavailable (no finished items in window)",
		"work that came back: unavailable (no story created in window)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}

	ungated := throughput.Quality([]throughput.Item{item("sty_aaaaaaaa", planRoute(t))}, throughput.Returns{})
	if ungated.FirstPassByStep.Reason != throughput.ReasonNoPresentation || ungated.Rounds.Reason != throughput.ReasonNoPresentation {
		t.Errorf("no gated presentation = %+v / %+v", ungated.FirstPassByStep, ungated.Rounds)
	}

	gone := throughput.Unavailable("gone", "/gone", "repo unreadable: boom", window())
	check("unreadable repo", gone.Quality, "repo unreadable: boom", "repo unreadable: boom")
	raw, _ := json.Marshal(gone.Quality)
	if !strings.Contains(string(raw), `"redispatches":{"count":{"value":null,"reason":"repo unreadable: boom"}`) {
		t.Errorf("unreadable quality JSON must be null with a reason:\n%s", raw)
	}
}
