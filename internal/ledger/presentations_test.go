package ledger

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// invocation is a reviewer agent_invocation row on backlog→plan. attempt may be
// empty (a row written before invocation rows carried one).
func invocation(attempt, skill, outcome string) Entry {
	p := map[string]string{"from": "backlog", "to": "plan", "agent": "reviewer", "skill": skill}
	if attempt != "" {
		p["attempt"] = attempt
	}
	if outcome != "" {
		p["outcome"] = outcome
	}
	return row(KindAgentInvocation, p)
}

// sty_46e950ab AC2: a seat attempt that ends at the turn cap writes only an
// invocation row. It is a presentation (counted, no verdict) and no reject.
func TestMaxTurnsAttemptIsAPresentationButNotAReject(t *testing.T) {
	entries := []Entry{
		invocation("att_1", "rev", ""), reject("att_1", "rev", "no"),
		invocation("att_2", "rev", "max-turns"),
		invocation("att_3", "rev", ""), accept("att_3", "rev"),
	}
	if a, r := EdgeRounds(entries, "backlog", "plan"); a != 1 || r != 1 {
		t.Errorf("EdgeRounds = %d accepted / %d rejected, want 1 / 1 — a max-turns attempt is no reject", a, r)
	}
	if got := CountRejectedRounds(entries, "backlog", "plan"); got.Rounds != 1 {
		t.Errorf("CountRejectedRounds = %d, want 1", got.Rounds)
	}
	ps := EdgePresentations(entries, "backlog", "plan")
	if len(ps) != 3 {
		t.Fatalf("presentations = %d, want 3: %+v", len(ps), ps)
	}
	want := []struct {
		attempt                       string
		accepted, rejected, noVerdict bool
		invocations                   int
	}{
		{"att_1", false, true, false, 1},
		{"att_2", false, false, true, 1},
		{"att_3", true, false, false, 1},
	}
	for i, w := range want {
		p := ps[i]
		if p.Attempt != w.attempt || p.Accepted != w.accepted || p.Rejected != w.rejected || p.NoVerdict != w.noVerdict || len(p.Invocations) != w.invocations {
			t.Errorf("presentation %d = %+v, want %+v", i, p, w)
		}
	}
}

// A presentation that fanned out to several seats is one presentation, and a
// lifecycle hook's rows, another edge's rows and a performer's rows are not it.
func TestEdgePresentationsGroupsSeatsAndSkipsOtherRows(t *testing.T) {
	performer := row(KindAgentInvocation, map[string]string{"from": "backlog", "to": "plan", "agent": "planner", "attempt": "att_1"})
	hook := row(KindAgentInvocation, map[string]string{"from": "backlog", "to": "plan", "agent": "reviewer", "operation": "create_review"})
	elsewhere := row(KindAgentInvocation, map[string]string{"from": "plan", "to": "in_progress", "agent": "reviewer", "attempt": "att_x"})
	entries := []Entry{
		performer, hook, elsewhere,
		invocation("att_1", "rev", ""), invocation("att_1", "rev", "unavailable"), reject("att_1", "rev", "no"),
	}
	ps := EdgePresentations(entries, "backlog", "plan")
	if len(ps) != 1 || len(ps[0].Invocations) != 2 || len(ps[0].Verdicts) != 1 {
		t.Fatalf("one presentation with two seat rows and one verdict, got %+v", ps)
	}
	if got := GatedEdges(entries); !reflect.DeepEqual(got, [][2]string{{"plan", "in_progress"}, {"backlog", "plan"}}) {
		t.Errorf("GatedEdges = %v, want each edge once in first-appearance order, hook and performer rows left out", got)
	}
}

// Rows written before invocation rows carried an attempt join the run whose
// verdict names the same skill at the same instant. One gate run stamps every row
// it writes with one instant and rows that share one read back in id order, so a
// legacy invocation can sit AFTER its verdict: the instant and skill, not
// adjacency, join it. A legacy row no verdict matches — the step summariser's —
// is no presentation.
func TestEdgePresentationsJoinsALegacyInvocationByInstantAndSkill(t *testing.T) {
	at := func(e Entry, sec int) Entry { e.CreatedAt = time.Unix(int64(1000+sec), 0).UTC(); return e }
	entries := []Entry{
		at(reject("att_1", "rev", "no"), 1), at(invocation("", "rev", ""), 1), // verdict first within the run
		at(accept("att_2", "rev"), 2), at(invocation("", "rev", ""), 2),
		at(invocation("", "satelle-step-summary", ""), 2), // same instant, a skill nobody judged
		at(invocation("", "rev", ""), 3),                  // no verdict shares this instant
	}
	ps := EdgePresentations(entries, "backlog", "plan")
	if len(ps) != 2 {
		t.Fatalf("presentations = %d, want the 2 that wrote a verdict: %+v", len(ps), ps)
	}
	if ps[0].Attempt != "att_1" || len(ps[0].Invocations) != 1 || ps[1].Attempt != "att_2" || len(ps[1].Invocations) != 1 {
		t.Errorf("each legacy invocation joins the run that shares its instant and skill: %+v", ps)
	}
}

// A bundle's one row names every skill it judged, joined by "+".
func TestEdgePresentationsJoinsALegacyBundleRowByAnyOfItsSkills(t *testing.T) {
	at := func(e Entry) Entry { e.CreatedAt = time.Unix(1000, 0).UTC(); return e }
	entries := []Entry{at(accept("att_1", "rev-b")), at(invocation("", "rev-a+rev-b", ""))}
	ps := EdgePresentations(entries, "backlog", "plan")
	if len(ps) != 1 || len(ps[0].Invocations) != 1 {
		t.Fatalf("the bundle row joins the run: %+v", ps)
	}
}

// The step summariser's row, marked on a row written now, is never a seat.
func TestEdgePresentationsSkipsTheStepSummariser(t *testing.T) {
	summary := row(KindAgentInvocation, map[string]string{"from": "backlog", "to": "plan", "agent": "reviewer", "skill": "satelle-step-summary", "attempt": "att_1"})
	summary.Payload = json.RawMessage(`{"from":"backlog","to":"plan","agent":"reviewer","skill":"satelle-step-summary","attempt":"att_1","step_summary":true}`)
	entries := []Entry{invocation("att_1", "rev", ""), summary, accept("att_1", "rev")}
	ps := EdgePresentations(entries, "backlog", "plan")
	if len(ps) != 1 || len(ps[0].Invocations) != 1 {
		t.Fatalf("only the gate's own seat counts: %+v", ps)
	}
}
