package costview

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// sty_51d0f74a: the performer side of convergence — per round the named
// performer's turns, tokens and duration, the blocking findings it was shown and
// how many it answered, and per edge the round it converged or stopped at. All of
// it is read from rows the binary already wrote.

const performerNoTurnsReason = "grok command: the run's output carried no turn count (num_turns)"

// performerEntry is a named performer's agent_invocation row on backlog→plan,
// shaped like verb.dispatchPayload writes it: its turn count lives under
// budget, never at the top level a reviewer's row uses.
func performerEntry(attempt string, budget map[string]any, extra map[string]any) ledger.Entry {
	p := map[string]any{
		"from": "backlog", "to": "plan", "agent": "planner", "skill": "plan",
		"attempt": attempt, "usage_available": true,
		"budget": budget,
	}
	for k, v := range extra {
		p[k] = v
	}
	return roundsEntry(ledger.KindAgentInvocation, p)
}

// performerRounds is backlog→plan presented three times. Round 1: one dispatch,
// 7 turns, nothing to answer yet. Round 2: a failed dispatch with no turn count
// and no answers, then a retry that answered the two presented findings (one
// adopted, one rebutted) and one it was never shown. Round 3: one dispatch, 3
// turns, shown one finding and answering none; the gate accepts.
func performerRounds() []ledger.Entry {
	return []ledger.Entry{
		performerEntry("att_1", map[string]any{"turns": 7}, map[string]any{"tokens_in": 1000, "tokens_out": 200, "duration_ms": 3000}),
		invocationEntry("att_1", "plan-review", "", "", map[string]any{"turns": 4}),
		verdictEntry(ledger.KindReviewReject, "att_1", "plan-review", nil),

		performerEntry("att_2", map[string]any{"turns_unavailable_reason": performerNoTurnsReason},
			map[string]any{"tokens_in": 500, "tokens_out": 100, "duration_ms": 2000, "findings_offered": []string{"plan-review#1", "rev-b#1"}}),
		performerEntry("att_2", map[string]any{"turns": 5}, map[string]any{
			"tokens_in": 300, "tokens_out": 50, "duration_ms": 1000,
			"findings_offered": []string{"plan-review#1", "rev-b#1"},
			"finding_answers": []map[string]any{
				{"finding": "plan-review#1", "answer": "adopted"},
				{"finding": "rev-b#1", "answer": "rebutted", "evidence": "engine.go"},
				{"finding": "z#9", "answer": "adopted"}, // never presented
			},
		}),
		invocationEntry("att_2", "plan-review", "", "", map[string]any{"turns": 5}),
		verdictEntry(ledger.KindReviewReject, "att_2", "plan-review", nil),

		performerEntry("att_3", map[string]any{"turns": 3}, map[string]any{
			"tokens_in": 100, "tokens_out": 20, "duration_ms": 500,
			"findings_offered": []string{"plan-review#2"},
		}),
		invocationEntry("att_3", "plan-review", "", "", map[string]any{"turns": 2}),
		verdictEntry(ledger.KindReviewAccept, "att_3", "plan-review", nil),
	}
}

func TestReviewRoundsReportsThePerformerOfEachRound(t *testing.T) {
	e := ReviewRounds("sty_a", "plan", performerRounds()).Edges[0]
	if len(e.Rounds) != 3 {
		t.Fatalf("rounds = %d, want 3", len(e.Rounds))
	}
	for i, r := range e.Rounds {
		if r.Round != i+1 {
			t.Errorf("round %d numbered %d", i+1, r.Round)
		}
		if len(r.Seats) != 1 {
			t.Errorf("round %d keeps its gate seat beside the performer: %+v", i+1, r.Seats)
		}
	}

	r1 := e.Rounds[0].Performer
	if r1 == nil || r1.Agent != "planner" || r1.Dispatches != 1 || r1.Turns == nil || *r1.Turns != 7 ||
		r1.TokensIn != 1000 || r1.TokensOut != 200 || r1.DurationMs != 3000 {
		t.Errorf("round 1 performer = %+v, want 1 dispatch, 7 turns (read from budget), 1000/200 tokens, 3s", r1)
	}
	if r1.FindingsPresented != 0 || r1.Answered != 0 {
		t.Errorf("a first presentation was shown no finding: %+v", r1)
	}

	// A retry is summed; a dispatch with no turn count leaves the sum unavailable
	// with the adapter's own reason, never a zero or the legacy reason.
	r2 := e.Rounds[1].Performer
	if r2 == nil || r2.Dispatches != 2 || r2.TokensIn != 800 || r2.TokensOut != 150 || r2.DurationMs != 3000 {
		t.Fatalf("round 2 performer = %+v, want 2 dispatches summed to 800/150 tokens, 3s", r2)
	}
	if r2.Turns != nil || r2.TurnsUnavailableReason != performerNoTurnsReason {
		t.Errorf("round 2 turns = %v (%q), want unavailable with the adapter's reason", r2.Turns, r2.TurnsUnavailableReason)
	}
	// Presented and answered come from the last dispatch that was offered findings,
	// so the retry is not counted twice; the answer to z#9 is not counted at all.
	if r2.FindingsPresented != 2 || r2.Answered != 2 || r2.Adopted != 1 || r2.Rebutted != 1 {
		t.Errorf("round 2 findings = presented %d answered %d adopted %d rebutted %d, want 2/2/1/1",
			r2.FindingsPresented, r2.Answered, r2.Adopted, r2.Rebutted)
	}

	r3 := e.Rounds[2].Performer
	if r3 == nil || r3.FindingsPresented != 1 || r3.Answered != 0 || r3.Adopted != 0 || r3.Rebutted != 0 {
		t.Errorf("round 3 performer = %+v, want 1 finding presented, none answered", r3)
	}
}

// A round with no performer row has no performer, and a performer row with no
// attempt id or an unknown one is joined to no round.
func TestReviewRoundsRoundWithoutPerformerHasNone(t *testing.T) {
	rep := ReviewRounds("sty_a", "", []ledger.Entry{
		performerEntry("", map[string]any{"turns": 1}, nil),
		performerEntry("att_orphan", map[string]any{"turns": 1}, nil),
		invocationEntry("att_1", "plan-review", "", "", nil),
		verdictEntry(ledger.KindReviewAccept, "att_1", "plan-review", nil),
	})
	if got := rep.Edges[0].Rounds[0].Performer; got != nil {
		t.Errorf("performer = %+v, want none", got)
	}
}

// A performer row with no turns anywhere says the row predates turn counting.
func TestReviewRoundsPerformerLegacyTurnsAreUnavailableWithAReason(t *testing.T) {
	rep := ReviewRounds("sty_a", "", []ledger.Entry{
		roundsEntry(ledger.KindAgentInvocation, map[string]any{"from": "backlog", "to": "plan", "agent": "planner", "attempt": "att_1"}),
		invocationEntry("att_1", "plan-review", "", "", nil),
		verdictEntry(ledger.KindReviewAccept, "att_1", "plan-review", nil),
	})
	p := rep.Edges[0].Rounds[0].Performer
	if p == nil || p.Turns != nil || p.TurnsUnavailableReason != legacyCountsReason {
		t.Errorf("performer = %+v, want unavailable turns with the legacy reason", p)
	}
}

// DecodeRow reads a performer row's turns from its budget report, and prefers the
// top-level count a reviewer's row writes.
func TestDecodeRowReadsTurnsFromTheBudgetReport(t *testing.T) {
	row, ok := DecodeRow(performerEntry("att_1", map[string]any{"turns": 7}, nil))
	if !ok || row.Turns == nil || *row.Turns != 7 {
		t.Errorf("budget turns: %+v ok=%v, want 7", row, ok)
	}
	row, _ = DecodeRow(performerEntry("att_1", map[string]any{"turns_unavailable_reason": performerNoTurnsReason}, nil))
	if row.Turns != nil || row.TurnsUnavailableReason != performerNoTurnsReason {
		t.Errorf("budget reason: %+v, want unavailable with the budget's reason", row)
	}
	row, _ = DecodeRow(performerEntry("att_1", map[string]any{"turns": 7}, map[string]any{"turns": 2}))
	if row.Turns == nil || *row.Turns != 2 {
		t.Errorf("a top-level count wins: %+v", row)
	}
}

// An edge converges at its first accepted presentation; one never accepted whose
// story has left the from-state stopped at its last round; otherwise it is open.
func TestEdgeConvergenceStates(t *testing.T) {
	rejectTwice := performerRounds()[:7] // rounds 1 and 2, both rejected
	cases := []struct {
		name, status, state string
		entries             []ledger.Entry
		converged, stopped  int
	}{
		{"accepted at round 3", "plan", EdgeConverged, performerRounds(), 3, 0},
		{"left the from-state", "blocked", EdgeStopped, rejectTwice, 0, 2},
		{"still in the from-state", "backlog", EdgeOpen, rejectTwice, 0, 0},
		{"status unknown", "", EdgeOpen, rejectTwice, 0, 0},
	}
	for _, c := range cases {
		e := ReviewRounds("sty_a", c.status, c.entries).Edges[0]
		if e.State != c.state || e.ConvergedAt != c.converged || e.StoppedAt != c.stopped {
			t.Errorf("%s: state %q converged %d stopped %d, want %q %d %d", c.name, e.State, e.ConvergedAt, e.StoppedAt, c.state, c.converged, c.stopped)
		}
	}
}

func TestFamilyRoundsTotalsThePerformerFiguresAndEdgeStates(t *testing.T) {
	a := ReviewRounds("sty_child_a", "plan", performerRounds())
	b := ReviewRounds("sty_child_b", "blocked", performerRounds()[:7])
	c := ReviewRounds("sty_child_c", "backlog", performerRounds()[:3])
	got := FamilyRounds("sty_root", []ReviewRoundsReport{ReviewRounds("sty_root", "", nil), a, b, c}).Total[0]

	// a: 7 + (round 2 unavailable) + 3 turns; b: 7 + unavailable; c: 7.
	if got.PerformerTurns != 7+3+7+7 || got.UnavailablePerformerTurns != 2 {
		t.Errorf("performer turns = %d (%d unavailable), want 24 (2 unavailable)", got.PerformerTurns, got.UnavailablePerformerTurns)
	}
	// Tokens in: a 1000+800+100, b 1000+800, c 1000.
	if got.PerformerTokensIn != 1900+1800+1000 || got.PerformerTokensOut != 370+350+200 || got.PerformerDurationMs != 6500+6000+3000 {
		t.Errorf("performer tokens/duration = %d/%d %dms", got.PerformerTokensIn, got.PerformerTokensOut, got.PerformerDurationMs)
	}
	if got.FindingsPresented != 3+2+0 || got.Answered != 2+2 || got.Adopted != 1+1 || got.Rebutted != 1+1 {
		t.Errorf("findings = presented %d answered %d adopted %d rebutted %d, want 5/4/2/2",
			got.FindingsPresented, got.Answered, got.Adopted, got.Rebutted)
	}
	if got.Converged != 1 || got.Stopped != 1 || got.Open != 1 {
		t.Errorf("edge states = converged %d stopped %d open %d, want 1/1/1", got.Converged, got.Stopped, got.Open)
	}
}
