package web

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// routeSpec is a resolved route: a start, three work stages, a terminal done, the
// resume park (reviewer role, from="*") and a cancel sink (reviewer role, no
// from). The classification the stage builder reads is all in the Spec — the test
// names no state the production code does not receive.
func routeSpec() wfdot.Spec {
	return wfdot.Spec{States: []wfdot.State{
		{Name: "backlog", Shape: "Mdiamond"},
		{Name: "plan", Agent: "planner"},
		{Name: "in_progress", Agent: "coder"},
		{Name: "integration", Agent: "executor"},
		{Name: "done", Shape: "Msquare"},
		{Name: "blocked", Agent: "reviewer", From: []string{"*"}},
		{Name: "cancelled", Agent: "reviewer"},
	}}
}

// ddbeLedger is the sty_ddbe2669 shape: backlog→plan took 6 rounds (5 rejected,
// 1 accepted), with a park into blocked and its recovery in the middle, and the
// first rejected round had TWO rejecting reviewers; then plan→in_progress took 1
// accepted round with both reviewers accepting.
func ddbeLedger() []ledger.Entry {
	const bp = "backlog"
	return []ledger.Entry{
		evA(ledger.KindReviewReject, bp, "plan", "a1"),
		evA(ledger.KindReviewReject, bp, "plan", "a1"), // second reviewer, same round
		evA(ledger.KindReviewAccept, bp, "plan", "a2"), // dissent in a rejected round
		evA(ledger.KindReviewReject, bp, "plan", "a2"),
		evA(ledger.KindReviewReject, bp, "plan", "a3"),
		ev(ledger.KindStatusTransition, bp, "blocked"), // park
		ev(ledger.KindStatusTransition, "blocked", bp), // recover
		evA(ledger.KindReviewReject, bp, "plan", "a4"),
		evA(ledger.KindReviewReject, bp, "plan", "a5"),
		evA(ledger.KindReviewAccept, bp, "plan", "a6"),
		evA(ledger.KindReviewAccept, bp, "plan", "a6"),
		ev(ledger.KindStatusTransition, bp, "plan"),
		evA(ledger.KindReviewAccept, "plan", "in_progress", "b1"),
		evA(ledger.KindReviewAccept, "plan", "in_progress", "b1"),
		ev(ledger.KindStatusTransition, "plan", "in_progress"),
	}
}

func checkDdbe(t *testing.T, spec wfdot.Spec) {
	t.Helper()
	stages, gate := buildStages(ddbeLedger(), "in_progress", false, noStep, spec)
	if got, want := names(stages), []string{"plan", "in_progress"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stages = %v, want exactly %v (no blocked, no backlog)", got, want)
	}
	plan := stages[0]
	if plan.Accepted != 1 || plan.Rejected != 5 || plan.Parked != 1 || plan.State != "done" {
		t.Errorf("plan = %+v, want 1 accepted, 5 rejected, parked 1, done", plan)
	}
	cur := stages[1]
	if cur.State != "current" || cur.Accepted != 1 || cur.Rejected != 0 {
		t.Errorf("in_progress = %+v, want current with 1 accepted", cur)
	}
	if gate == nil || gate.Accepted != 1 || gate.Rejected != 0 {
		t.Errorf("gate = %+v, want the entry gate's 1 accepted", gate)
	}
}

// AC1/AC2/AC3: the ddbe2669 fixture, with the route resolved…
func TestStagesDdbeFixtureResolvedRoute(t *testing.T) { checkDdbe(t, routeSpec()) }

// …and with no Spec at all (the degraded fallback reads the A→X→A shape).
func TestStagesDdbeFixtureNoRoute(t *testing.T) { checkDdbe(t, wfdot.Spec{}) }

// AC2: however many reviewers reject in one attempt, it is ONE rejected round.
func TestStagesTwoRejectingReviewersAreOneRound(t *testing.T) {
	entries := []ledger.Entry{
		evA(ledger.KindReviewReject, "backlog", "plan", "x1"),
		evA(ledger.KindReviewReject, "backlog", "plan", "x1"),
		evA(ledger.KindReviewReject, "backlog", "plan", "x1"),
		ev(ledger.KindStatusTransition, "backlog", "plan"),
	}
	stages, _ := buildStages(entries, "plan", false, noStep, routeSpec())
	if s := stageNamed(stages, "plan"); s.Rejected != 1 || s.Accepted != 0 {
		t.Fatalf("plan = %+v, want 1 rejected round from three rejecting rows", s)
	}
	if a, r := ledger.EdgeRounds(entries, "backlog", "plan"); a != 0 || r != 1 {
		t.Errorf("EdgeRounds = %d/%d, want 0/1", a, r)
	}
}

// AC4: rejected rounds before an accepted one on the same edge stay red; the
// accept does not turn them green.
func TestStagesRejectedRoundsStayRedAfterAnAccept(t *testing.T) {
	var entries []ledger.Entry
	for _, id := range []string{"r1", "r2", "r3", "r4", "r5"} {
		entries = append(entries, evA(ledger.KindReviewReject, "backlog", "plan", id))
	}
	entries = append(entries,
		evA(ledger.KindReviewAccept, "backlog", "plan", "r6"),
		ev(ledger.KindStatusTransition, "backlog", "plan"))
	stages, _ := buildStages(entries, "done", false, noStep, wfdot.Spec{})
	if s := stageNamed(stages, "plan"); s.Accepted != 1 || s.Rejected != 5 {
		t.Fatalf("plan = %+v, want 1 accepted / 5 rejected", s)
	}
}

// AC3: a transition into the cancel sink is not a park note, and a cancelled
// story is finished — its chip never pulses and it has no outgoing-gate badge.
func TestStagesCancelIsNotAParkAndNeverPulses(t *testing.T) {
	entries := []ledger.Entry{
		evA(ledger.KindReviewAccept, "backlog", "plan", "c1"),
		ev(ledger.KindStatusTransition, "backlog", "plan"),
		evA(ledger.KindReviewReject, "plan", "in_progress", "c2"), // a presented outgoing edge
		ev(ledger.KindStatusTransition, "plan", "cancelled"),
	}
	stages, gate := buildStages(entries, "cancelled", false, noStep, routeSpec())
	for _, s := range stages {
		if s.Parked != 0 {
			t.Errorf("cancel produced a park note: %+v", s)
		}
		if s.State == "current" {
			t.Errorf("a cancelled story must show no pulse: %+v", stages)
		}
	}
	if got, want := names(stages), []string{"plan", "cancelled"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stages = %v, want %v (the sink only as the final chip)", got, want)
	}
	// Finished: the badge is the ENTRY gate (cancelled had none) — never the
	// rejected outgoing plan→in_progress round.
	if gate != nil {
		t.Errorf("gate = %+v, want none for a finished story with no entry-gate rows", gate)
	}
}

// AC3: with no Spec the finished decision reads the workitem lifecycle names.
func TestStagesDoneWithoutRouteNeverPulses(t *testing.T) {
	entries := []ledger.Entry{
		evA(ledger.KindReviewAccept, "backlog", "in_progress", "d1"),
		ev(ledger.KindStatusTransition, "backlog", "in_progress"),
		evA(ledger.KindReviewAccept, "in_progress", workitem.StatusDone, "d2"),
		ev(ledger.KindStatusTransition, "in_progress", workitem.StatusDone),
	}
	stages, gate := buildStages(entries, workitem.StatusDone, false, noStep, wfdot.Spec{})
	for _, s := range stages {
		if s.State == "current" {
			t.Fatalf("a done story with no route must show no pulse: %+v", stages)
		}
	}
	if gate == nil || gate.Accepted != 1 {
		t.Errorf("gate = %+v, want the done entry gate's 1 accepted", gate)
	}
}

// AC3: a park the story is still in names the park as the current chip, with no
// counts, and the stage whose gate was in flight carries the note.
func TestStagesStillParked(t *testing.T) {
	entries := []ledger.Entry{
		evA(ledger.KindReviewAccept, "backlog", "plan", "p1"),
		ev(ledger.KindStatusTransition, "backlog", "plan"),
		evA(ledger.KindReviewReject, "plan", "in_progress", "p2"),
		ev(ledger.KindStatusTransition, "plan", "blocked"),
	}
	stages, _ := buildStages(entries, "blocked", false, noStep, routeSpec())
	if got, want := names(stages), []string{"plan", "blocked"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stages = %v, want %v", got, want)
	}
	if stages[0].Parked != 1 {
		t.Errorf("plan = %+v, want the park noted on the stage the story was in", stages[0])
	}
	last := stages[len(stages)-1]
	if last.Name != "blocked" || last.State != "current" || last.Accepted+last.Rejected+last.Parked != 0 {
		t.Errorf("last = %+v, want a bare current blocked chip", last)
	}
}

// AC5: the STATUS badge — mid-gate, entry gate, and no rows.
func TestGateBadgeThreeCases(t *testing.T) {
	enter := []ledger.Entry{
		evA(ledger.KindReviewReject, "backlog", "plan", "g0"),
		evA(ledger.KindReviewAccept, "backlog", "plan", "g1"),
		ev(ledger.KindStatusTransition, "backlog", "plan"),
	}
	// Entry gate: nothing presented since entering plan.
	_, gate := buildStages(enter, "plan", false, noStep, routeSpec())
	if gate == nil || gate.Accepted != 1 || gate.Rejected != 1 || gate.Edge != "backlog → plan" {
		t.Errorf("entry-gate badge = %+v, want 1 accepted 1 rejected on backlog → plan", gate)
	}
	// Mid-gate: an outgoing edge has been presented since entering plan.
	mid := append(append([]ledger.Entry(nil), enter...),
		evA(ledger.KindReviewReject, "plan", "in_progress", "g2"),
		evA(ledger.KindReviewReject, "plan", "in_progress", "g2"),
		evA(ledger.KindReviewReject, "plan", "in_progress", "g3"))
	_, gate = buildStages(mid, "plan", false, noStep, routeSpec())
	if gate == nil || gate.Accepted != 0 || gate.Rejected != 2 || gate.Edge != "plan → in_progress" {
		t.Errorf("mid-gate badge = %+v, want 2 rejected on plan → in_progress", gate)
	}
	// No review rows → no badge.
	bare := []ledger.Entry{ev(ledger.KindStatusTransition, "backlog", "plan")}
	if _, gate = buildStages(bare, "plan", false, noStep, routeSpec()); gate != nil {
		t.Errorf("no review rows: badge = %+v, want none", gate)
	}
	// An outgoing round from BEFORE the story entered its status is not mid-gate.
	stale := []ledger.Entry{
		evA(ledger.KindReviewReject, "plan", "in_progress", "s1"),
		evA(ledger.KindReviewAccept, "backlog", "plan", "s2"),
		ev(ledger.KindStatusTransition, "backlog", "plan"),
	}
	if _, gate = buildStages(stale, "plan", false, noStep, routeSpec()); gate == nil || gate.Edge != "backlog → plan" {
		t.Errorf("stale outgoing round: badge = %+v, want the entry gate", gate)
	}
}

// AC6: a category that resolved no route spine (empty Spec, every depth 0) orders
// its stages by first entry in the ledger, each once.
func TestStagesNoSpineOrdersByLedger(t *testing.T) {
	stages, _ := buildStages(ddbeLedger(), "in_progress", false, noStep, wfdot.Spec{})
	if got, want := names(stages), []string{"plan", "in_progress"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stages = %v, want %v", got, want)
	}
}

// AC1: the rendered row carries stage chips and no numbered review-light circle.
func TestStagesRenderNoNumberedLights(t *testing.T) {
	stages, gate := buildStages(ddbeLedger(), "in_progress", false, noStep, routeSpec())
	it := workitem.Item{ID: "sty_ddbe2669", Kind: workitem.KindStory, Title: "T", Status: "in_progress"}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "workitemRows", []rowVM{{Item: it, Stages: stages, Gate: gate}}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if strings.Contains(html, "review-light") {
		t.Errorf("a numbered review-light circle was rendered:\n%s", html)
	}
	for _, want := range []string{
		`class="stage-chip stage-done"`, `class="stage-chip stage-current"`,
		`<b class="ok">1</b>`, `<b class="rej">5</b>`, "parked 1", `class="gate-badge"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered row missing %q:\n%s", want, html)
		}
	}
	// The counts are told apart by colour class alone — no tick or cross glyph in
	// any stage chip or gate badge — and each carries its meaning as a title.
	if strings.ContainsAny(html, "✓✗") {
		t.Errorf("a stage chip or gate badge rendered a tick/cross glyph:\n%s", html)
	}
	for _, want := range []string{
		`class="stage-chip stage-done" title="`,
		`class="gate-badge" title="`,
		"accepted round", "rejected rounds",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered row missing title text %q:\n%s", want, html)
		}
	}
	if !strings.Contains(html, "1 accepted round, 5 rejected rounds, parked once") {
		t.Errorf("tooltip should spell the totals in words:\n%s", html)
	}
}

func TestFinishedRule(t *testing.T) {
	spec := routeSpec()
	for status, want := range map[string]bool{"done": true, "cancelled": true, "plan": false, "blocked": false, "backlog": false} {
		if got := finished(spec, status); got != want {
			t.Errorf("finished(route, %q) = %v, want %v", status, got, want)
		}
	}
	for status, want := range map[string]bool{"done": true, "cancelled": true, "plan": false, "blocked": false} {
		if got := finished(wfdot.Spec{}, status); got != want {
			t.Errorf("finished(no route, %q) = %v, want %v", status, got, want)
		}
	}
}
