package substratereview

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// fixtureLedger is the history AC2 is proven over. Gate G (the satelle-g skill
// gating entry to plan) and gate H (satelle-h, same edge):
//
//   - sty_a: G rejects twice (R1, R2) under M1; a definition edit follows R1
//     (amended), the presentation after R2 accepts with no edit (reversed); the
//     planner re-runs after the rejected exit (rework). H accepts the last round
//     single-seat. The in-loop session ran M0.
//   - sty_b: a two-seat panel round on G under M2: reviewer accepts,
//     reviewer-gpt rejects with notes N, and the round passes (dissent).
//   - sty_c: G rejects (R3) and the story parks to blocked (a reject-budget park).
//   - sty_d: a presentation of G whose seat returned no verdict (flaky), then an
//     accepted re-presentation.
func fixtureLedger() map[string][]ledger.Entry {
	t0 := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	n := 0
	row := func(story, kind string, p map[string]any) ledger.Entry {
		n++
		b, _ := json.Marshal(p)
		return ledger.Entry{ID: story + "-" + kind, StoryID: story, Kind: kind, Payload: b, CreatedAt: t0.Add(time.Duration(n) * time.Second)}
	}
	edge := func(skill, attempt, model string, extra map[string]any) map[string]any {
		p := map[string]any{"from": "backlog", "to": "plan", "skill": skill, "attempt": attempt, "model": model, "model_resolved": model}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	inv := func(story, skill, attempt, model string) ledger.Entry {
		return row(story, ledger.KindAgentInvocation, edge(skill, attempt, model, map[string]any{"agent": "reviewer"}))
	}
	return map[string][]ledger.Entry{
		"sty_a": {
			row("sty_a", ledger.KindSessionModel, map[string]any{"role": "in-loop", "model": "M0"}),
			row("sty_a", ledger.KindReviewReject, edge("satelle-g", "a1", "M1", map[string]any{"notes": "R1"})),
			inv("sty_a", "satelle-g", "a1", "M1"),
			row("sty_a", ledger.KindDefinitionEdited, map[string]any{"field": "acceptance_criteria", "old": "x", "new": "y"}),
			row("sty_a", ledger.KindReviewReject, edge("satelle-g", "a2", "M1", map[string]any{"notes": "R2"})),
			inv("sty_a", "satelle-g", "a2", "M1"),
			row("sty_a", ledger.KindAgentInvocation, map[string]any{"agent": "planner", "skill": "plan", "from": "backlog", "to": "backlog"}),
			row("sty_a", ledger.KindReviewAccept, edge("satelle-g", "a3", "M1", nil)),
			inv("sty_a", "satelle-g", "a3", "M1"),
			row("sty_a", ledger.KindReviewAccept, edge("satelle-h", "a3", "M1", nil)),
			inv("sty_a", "satelle-h", "a3", "M1"),
			row("sty_a", ledger.KindStatusTransition, map[string]any{"from": "backlog", "to": "plan"}),
		},
		"sty_b": {
			row("sty_b", ledger.KindReviewAccept, map[string]any{"from": "backlog", "to": "plan", "skill": "satelle-g", "attempt": "b1",
				"combine": "satelle-panel-majority", "notes": "dissent: reviewer-gpt",
				"seats": []map[string]any{
					{"seat": "reviewer", "accept": true, "notes": "fine"},
					{"seat": "reviewer-gpt", "accept": false, "notes": "N"},
				}}),
			inv("sty_b", "satelle-g", "b1", "M2"),
			inv("sty_b", "satelle-g", "b1", "M2"),
		},
		"sty_c": {
			row("sty_c", ledger.KindReviewReject, edge("satelle-g", "c1", "M1", map[string]any{"notes": "R3"})),
			inv("sty_c", "satelle-g", "c1", "M1"),
			row("sty_c", ledger.KindStatusTransition, map[string]any{"from": "backlog", "to": "blocked"}),
		},
		"sty_d": {
			inv("sty_d", "satelle-g", "d1", "M1"),
			row("sty_d", ledger.KindReviewAccept, edge("satelle-g", "d2", "M1", nil)),
			inv("sty_d", "satelle-g", "d2", "M1"),
		},
	}
}

var fixtureBound = map[string]string{"reviewer": "B1", "reviewer-gpt": "B2"}

func TestEvidenceCountsSharesReasonsStoriesAndModels(t *testing.T) {
	h := Fold(fixtureLedger(), EvidenceOptions{ParkStates: []string{"blocked"}, Bound: fixtureBound})
	if h.Orchestrator != "M0" {
		t.Errorf("orchestrator model = %q, want M0 (the in-loop session_model row)", h.Orchestrator)
	}
	ev := h.For(Selector{State: "plan", Skill: "satelle-g"}, []string{"reviewer", "reviewer-gpt"})

	check := func(name string, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
	check("presentations", ev.Presentations, 7)
	check("rejections", ev.Rejections, 3)
	check("reasons", ev.Reasons, []string{"R1", "R2", "R3"})
	check("amended", ev.Amended, 1)
	check("reversed", ev.Reversed, 1)
	check("re-presented to accept", ev.Represented, 0)
	share := 100.0 / 3
	check("amended share", ev.AmendedShare, share)
	check("reversed share", ev.ReversedShare, share)
	check("re-presented share", ev.RepresentedShare, 0.0)
	check("parks", ev.Parks, 1)
	check("rework", ev.Rework, 1)
	check("flaky", ev.Flaky, 1)
	check("dissent recorded", ev.DissentRecorded, true)
	check("dissent", ev.Dissent, []Dissent{{Story: "sty_b", Seat: "reviewer-gpt", Model: "M2", Accepted: false, Notes: "N"}})
	check("stories", ev.Stories, []string{"sty_a", "sty_b", "sty_c", "sty_d"})
	// Per model: the seat models the rows recorded (M1, M2), never the in-loop
	// session's M0.
	check("models", ev.Models, []ModelRow{
		{Model: "M1", Invocations: 6, Accepts: 2, Rejections: 3, Stories: []string{"sty_a", "sty_c", "sty_d"}},
		{Model: "M2", Invocations: 2, Accepts: 1, Rejections: 1, Stories: []string{"sty_b"}},
	})
	check("current models", ev.CurrentModels, map[string]string{"reviewer": "B1", "reviewer-gpt": "B2"})

	// Gate H only ever ran single-seat: the ledger says nothing about dissent.
	evH := h.For(Selector{State: "plan", Skill: "satelle-h"}, nil)
	check("H presentations", evH.Presentations, 1)
	check("H dissent recorded", evH.DissentRecorded, false)
	if !contains(evH.Lines(), "dissent: not recorded") {
		t.Errorf("H lines should say dissent: not recorded:\n%v", evH.Lines())
	}

	// The panel seat as an agent item: its own verdict is the rejection.
	evSeat := h.For(Selector{Agent: "reviewer-gpt"}, []string{"reviewer-gpt"})
	check("seat rejections", evSeat.Rejections, 1)
	check("seat reasons", evSeat.Reasons, []string{"N"})
	check("seat dissent", len(evSeat.Dissent), 1)
	check("seat models", evSeat.Models, []ModelRow{{Model: "M2", Accepts: 0, Rejections: 1, Stories: []string{"sty_b"}}})
}

// A no-verdict presentation is flaky and never rework; a redispatch is rework
// and never flaky.
func TestFlakyAndReworkAreCountedApart(t *testing.T) {
	all := fixtureLedger()
	sel := Selector{State: "plan", Skill: "satelle-g"}

	flaky := Fold(map[string][]ledger.Entry{"sty_d": all["sty_d"]}, EvidenceOptions{}).For(sel, nil)
	if flaky.Flaky != 1 || flaky.Rework != 0 {
		t.Errorf("no-verdict presentation: flaky=%d rework=%d, want 1 and 0", flaky.Flaky, flaky.Rework)
	}
	rework := Fold(map[string][]ledger.Entry{"sty_a": all["sty_a"]}, EvidenceOptions{}).For(sel, nil)
	if rework.Rework != 1 || rework.Flaky != 0 {
		t.Errorf("redispatch: rework=%d flaky=%d, want 1 and 0", rework.Rework, rework.Flaky)
	}
	// The planner is the performer that re-ran: the rework is its too.
	planner := Fold(map[string][]ledger.Entry{"sty_a": all["sty_a"]}, EvidenceOptions{}).For(Selector{Agent: "planner"}, nil)
	if planner.Rework != 1 {
		t.Errorf("planner rework = %d, want 1", planner.Rework)
	}
}

func TestRulesEnumerateConstitutionLinksThenSessionPrinciples(t *testing.T) {
	constitution := "# c\n\n- one: [[rule-a]]\n- two: [[rule-b]] and [[sty_123]]\n\n```\n[[not-a-rule]]\n```\n"
	got := Rules(constitution, []string{"rule-s", "rule-a"})
	want := []Rule{{Name: "rule-a", Origin: "constitution", Line: 3}, {Name: "rule-b", Origin: "constitution", Line: 4}, {Name: "rule-s", Origin: "session"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rules = %#v, want %#v", got, want)
	}
}
