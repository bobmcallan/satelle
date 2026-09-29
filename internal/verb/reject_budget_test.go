package verb_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// sty_4cf2c585: a rejection returns to the session that requested the edge and the
// story stays in its SOURCE status; the destination step's reject_budget bounds
// the loop. The fixture is a TERMINAL destination (release → done), the edge the
// story is about: a terminal state is never re-entered, so a reject holds the
// story in release and the retry is admitted into the same edge.

const rejectBudgetDoneToml = `["*"]
obligations = ["raised", "coded", "released", "closed"]
park = { state = "blocked", gate = "park-gate" }
`

func rejectBudgetStepToml(closedKnobs string) string {
	return `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[released]
status = "release"
agent = "executor"
requires = ["coded"]

[closed]
status = "done"
reviewers = ["gate-a", "gate-b"]
terminal = true
requires = ["released"]
` + closedKnobs
}

func rejectBudgetWF(closedKnobs string) map[string]string {
	return routeHalves(rejectBudgetDoneToml, rejectBudgetStepToml(closedKnobs))
}

// twoReviewersReject is one presentation where both reviewers of the edge reject,
// with distinctive multi-line notes and reasoning that a summary would lose.
func twoReviewersReject(n int) verb.GateDecision {
	notes := func(skill string) string {
		return `round ` + string(rune('0'+n)) + ` ` + skill + `: "AC2" is unmet —
	fixture missing under tests/  (two spaces, a tab, a newline)`
	}
	return verb.GateDecision{Gated: true, Accept: false, Skill: "gate-a", Reviewers: []verb.ReviewerVerdict{
		{Skill: "gate-a", Accept: false, Notes: notes("gate-a"), Reasoning: "reasoning-a-" + string(rune('0'+n))},
		{Skill: "gate-b", Accept: false, Notes: notes("gate-b")},
	}}
}

// atRelease drives a fresh story through the ungated edges to release.
func atRelease(t *testing.T) string {
	t.Helper()
	it := newFeature(t)
	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	call(t, "story-set", map[string]any{"id": it.ID, "status": "release"})
	if got := statusOf(t, it.ID).Status; got != "release" {
		t.Fatalf("setup: status = %q, want release", got)
	}
	return it.ID
}

func ledgerEntries(t *testing.T, id string) []ledger.Entry {
	t.Helper()
	var all []ledger.Entry
	if err := json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": id}), &all); err != nil {
		t.Fatal(err)
	}
	return all
}

// AC1: the rejection comes back as the command's error carrying every reviewer's
// notes and reasoning byte-for-byte, and the story is still in its source status
// with no transition row written.
func TestRejectedEdgeReturnsReviewerNotesVerbatimAndHoldsSource(t *testing.T) {
	newReadinessRig(t, rejectBudgetWF("reject_budget = 2\n"), func(to string, n int) verb.GateDecision {
		if to == "done" {
			return twoReviewersReject(n)
		}
		return acceptAll()
	})
	id := atRelease(t)
	transitions := len(ledgerRows(t, id, ledger.KindStatusTransition))

	err := dispatchErr(t, "story-set", map[string]any{"id": id, "status": "done"})

	want := twoReviewersReject(1)
	for _, rv := range want.Reviewers {
		if !strings.Contains(err.Error(), "rejected by "+rv.Skill+": notes="+rv.Notes) {
			t.Errorf("the rejection must carry %s's notes verbatim, got:\n%v", rv.Skill, err)
		}
	}
	if !strings.Contains(err.Error(), " reasoning="+want.Reviewers[0].Reasoning) {
		t.Errorf("the rejection must carry the reasoning verbatim, got:\n%v", err)
	}
	if got := statusOf(t, id).Status; got != "release" {
		t.Errorf("status = %q, want release (a rejected edge holds the source status)", got)
	}
	if got := len(ledgerRows(t, id, ledger.KindStatusTransition)); got != transitions {
		t.Errorf("a rejected edge writes no status_transition: %d rows, want %d", got, transitions)
	}
}

// AC3: a budget declared on the TERMINAL step holds the story in release after a
// reject and admits the retry into the same edge; an accept then reaches done.
func TestTerminalBudgetHoldsInReleaseAndAdmitsRetry(t *testing.T) {
	r := newReadinessRig(t, rejectBudgetWF("reject_budget = 2\n"), func(to string, n int) verb.GateDecision {
		if to == "done" && n == 1 {
			return twoReviewersReject(n)
		}
		return acceptAll()
	})
	id := atRelease(t)

	dispatchErr(t, "story-set", map[string]any{"id": id, "status": "done"})
	if got := statusOf(t, id).Status; got != "release" {
		t.Fatalf("after the reject: status = %q, want release", got)
	}

	call(t, "story-set", map[string]any{"id": id, "status": "done"})
	if got := r.gater.calls["done"]; got != 2 {
		t.Errorf("the second release→done must be admitted to the gate: gate calls = %d, want 2", got)
	}
	if got := statusOf(t, id).Status; got != "done" {
		t.Errorf("after the accept: status = %q, want done", got)
	}
}

// AC2: the retry is admitted until the budget is spent; the next presentation is
// refused before any gate is paid for, with the last objection quoted, and the
// orchestrator's park from the source status is admitted.
func TestSpentBudgetRefusesQuotingLastObjectionAndParkIsAdmitted(t *testing.T) {
	r := newReadinessRig(t, rejectBudgetWF("reject_budget = 2\n"), func(to string, n int) verb.GateDecision {
		if to == "done" {
			return twoReviewersReject(n)
		}
		return acceptAll()
	})
	id := atRelease(t)

	for i := 1; i <= 2; i++ {
		err := dispatchErr(t, "story-set", map[string]any{"id": id, "status": "done"})
		if strings.Contains(err.Error(), "reject budget") {
			t.Fatalf("presentation %d is within the budget and must reach the gate, got: %v", i, err)
		}
	}
	rows := len(ledgerRows(t, id, ledger.KindReviewReject))

	err := dispatchErr(t, "story-set", map[string]any{"id": id, "status": "done"})
	for _, want := range []string{"edge rejected 2 times", "reject budget 2", "last objection", "round 2 gate-", "decision needed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must contain %q, got: %v", want, err)
		}
	}
	if got := r.gater.calls["done"]; got != 2 {
		t.Errorf("a spent budget refuses before the gate: gate calls = %d, want 2", got)
	}
	if got := len(ledgerRows(t, id, ledger.KindReviewReject)); got != rows {
		t.Errorf("a refused presentation writes no reject row: %d, want %d", got, rows)
	}
	if got := statusOf(t, id).Status; got != "release" {
		t.Fatalf("a refusal holds the source status, got %q", got)
	}

	call(t, "story-set", map[string]any{"id": id, "status": "blocked"})
	if got := statusOf(t, id).Status; got != "blocked" {
		t.Errorf("the park after a spent budget: status = %q, want blocked", got)
	}
}

// AC4: the budget counts REJECTED PRESENTATIONS of the edge, not reviewers and not
// attempts. One presentation that two reviewers reject — here one bundled session —
// is one round, so a budget of 2 still admits the next presentation.
func TestBundledRejectCountsOnceAgainstBudget(t *testing.T) {
	r := newReadinessRig(t, rejectBudgetWF("reject_budget = 2\n"), func(to string, n int) verb.GateDecision {
		if to != "done" {
			return acceptAll()
		}
		dec := twoReviewersReject(n)
		for i := range dec.Reviewers {
			dec.Reviewers[i].BundleID = "bun_fixture"
			dec.Reviewers[i].BundleSkills = []string{"gate-a", "gate-b"}
		}
		return dec
	})
	id := atRelease(t)

	dispatchErr(t, "story-set", map[string]any{"id": id, "status": "done"})

	if got := len(ledgerRows(t, id, ledger.KindReviewReject)); got != 2 {
		t.Fatalf("reject rows = %d, want 2 (one per reviewer verdict)", got)
	}
	if got := ledger.CountRejectedRounds(ledgerEntries(t, id), "release", "done").Rounds; got != 1 {
		t.Errorf("rounds = %d, want 1: a multi-gate edge rejected once decrements the budget by one", got)
	}
	err := dispatchErr(t, "story-set", map[string]any{"id": id, "status": "done"})
	if strings.Contains(err.Error(), "reject budget") {
		t.Errorf("one rejected round of a budget of 2 must not refuse the retry, got: %v", err)
	}
	if got := r.gater.calls["done"]; got != 2 {
		t.Errorf("gate calls = %d, want 2", got)
	}
}

// AC6: a step that declares no budget is unchanged — every presentation reaches
// the gate and fails with the reviewer's rejection, however many have failed. An
// explicit `reject_budget = 0` is refused at parse (wfdot readiness_test).
func TestUndeclaredBudgetNeverRefuses(t *testing.T) {
	r := newReadinessRig(t, rejectBudgetWF(""), func(to string, n int) verb.GateDecision {
		if to == "done" {
			return twoReviewersReject(n)
		}
		return acceptAll()
	})
	id := atRelease(t)

	for i := 1; i <= 5; i++ {
		err := dispatchErr(t, "story-set", map[string]any{"id": id, "status": "done"})
		if !strings.Contains(err.Error(), "rejected by gate-a") || strings.Contains(err.Error(), "reject budget") {
			t.Fatalf("presentation %d: want the reviewer's rejection and no budget refusal, got: %v", i, err)
		}
	}
	if got := r.gater.calls["done"]; got != 5 {
		t.Errorf("gate calls = %d, want 5 (no refusal before the gate)", got)
	}
	if got := statusOf(t, id).Status; got != "release" {
		t.Errorf("status = %q, want release", got)
	}
}
