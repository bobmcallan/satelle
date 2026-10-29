package verb_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_36b91fd1 AC2: the repo's first accepted epic-parent readiness round carries
// the advisors its readiness step declares, on either path that runs the round.
// "First" is read off the readiness_accept ledger rows, never the ready mark.

const advisedSkill = "satelle-workflow-advisor"

// advisedStepToml is the readiness fixture with the epic's ready step declaring
// an advisor and, like the shipped step, propose — so `story set --status ready`
// runs the same round `story ready` does.
func advisedStepToml(advise bool) string {
	extra := "propose = true\n"
	if advise {
		extra += `advise = { agent = "orchestrator", skill = "` + advisedSkill + `" }` + "\n"
	}
	return strings.Replace(readyStepToml("ready", readyReviewers), "waits_on_children = true\n", "waits_on_children = true\n"+extra, 1)
}

// advisedRig wires the fixture; the epic's own ready step has no reviewers.
func advisedRig(t *testing.T, advise bool) *readyRig {
	t.Helper()
	return advisedRigFor(t, advisedStepToml(advise))
}

func advisedRigFor(t *testing.T, stepToml string) *readyRig {
	t.Helper()
	r := newRigFor(t, stepToml)
	r.g.verd = func(_ workitem.Item, to string) verb.GateDecision {
		if to == "ready" {
			return verb.GateDecision{}
		}
		return acceptAll()
	}
	return r
}

// advisedEpic files an epic-parent and one child of its set.
func advisedEpic(t *testing.T, theme string) (epic, child workitem.Item) {
	t.Helper()
	epic = newStoryOf(t, "epic-parent", "epic "+theme, "epic:"+theme)
	child = newStoryOf(t, "feature", "child "+theme, "epic:"+theme)
	return epic, child
}

func wantAdvice(t *testing.T, what string, got []string, want bool) {
	t.Helper()
	if want && !slices.Equal(got, []string{advisedSkill}) {
		t.Errorf("%s: advice = %v, want [%s]", what, got, advisedSkill)
	}
	if !want && len(got) != 0 {
		t.Errorf("%s: advice = %v, want none", what, got)
	}
}

// setReadyAdvice presents the epic's readiness edge with `story set` and returns
// the advice its response carries.
func setReadyAdvice(t *testing.T, id string) []string {
	t.Helper()
	raw, err := dispatchRaw(t, "story-set", map[string]any{"id": id, "status": "ready"})
	if err != nil {
		t.Fatalf("story set %s --status ready: %v", id, err)
	}
	var res verb.SetResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.ID != id || res.Status != "ready" {
		t.Fatalf("the response must still be the record, got %+v", res.Item)
	}
	return res.Advise
}

func TestFirstEpicReadinessCarriesTheDeclaredAdvice(t *testing.T) {
	advisedRig(t, true)
	first, _ := advisedEpic(t, "a")

	res := readyCall(t, first.ID)[0]
	if res.Outcome != verb.ReadyMarked {
		t.Fatalf("the first epic must be readied: %+v", res)
	}
	wantAdvice(t, "first epic readiness", res.Advise, true)
	if len(res.Children) != 1 || len(res.Children[0].Advise) != 0 {
		t.Errorf("a story child carries no advice: %+v", res.Children)
	}

	wantAdvice(t, "the same epic readied again", readyCall(t, first.ID)[0].Advise, false)

	second, _ := advisedEpic(t, "b")
	res = readyCall(t, second.ID)[0]
	if res.Outcome != verb.ReadyMarked {
		t.Fatalf("the second epic must be readied: %+v", res)
	}
	wantAdvice(t, "a second epic", res.Advise, false)
}

func TestEpicReadinessWithNoAdviseDeclaredCarriesNone(t *testing.T) {
	advisedRig(t, false)
	epic, _ := advisedEpic(t, "a")
	res := readyCall(t, epic.ID)[0]
	if res.Outcome != verb.ReadyMarked {
		t.Fatalf("the epic must be readied: %+v", res)
	}
	wantAdvice(t, "no advise declared", res.Advise, false)
}

// A rejected round writes no readiness_accept row, so it neither advises nor
// uses up the first accepted round.
func TestRejectedEpicReadinessCarriesNoAdvice(t *testing.T) {
	r := advisedRig(t, true)
	epic, _ := advisedEpic(t, "a")
	r.g.verd = func(_ workitem.Item, to string) verb.GateDecision {
		if to == "ready" {
			return fourReviewers("no")
		}
		return acceptAll()
	}
	res := readyCall(t, epic.ID)[0]
	if res.Outcome != verb.ReadyNotAccepted {
		t.Fatalf("the epic round must be rejected: %+v", res)
	}
	wantAdvice(t, "a rejected round", res.Advise, false)

	r.g.verd = func(_ workitem.Item, to string) verb.GateDecision {
		if to == "ready" {
			return verb.GateDecision{}
		}
		return acceptAll()
	}
	wantAdvice(t, "the first accepted round after a rejected one", readyCall(t, epic.ID)[0].Advise, true)
}

// The advice rides the accepted epic round, not the mark: a first epic its child
// holds back still advises, and is not advised again once it is marked.
func TestHeldBackFirstEpicAdvisesOnce(t *testing.T) {
	r := advisedRig(t, true)
	epic, child := advisedEpic(t, "a")
	r.g.verd = func(it workitem.Item, to string) verb.GateDecision {
		switch {
		case to == "ready":
			return verb.GateDecision{}
		case it.ID == child.ID:
			return fourReviewers("no")
		}
		return acceptAll()
	}
	res := readyCall(t, epic.ID)[0]
	if res.Outcome != verb.ReadyNotAccepted || !strings.Contains(res.Reason, child.ID) || hasMark(t, epic.ID) {
		t.Fatalf("the child must hold the epic back unmarked: %+v", res)
	}
	wantAdvice(t, "a first epic held back by its child", res.Advise, true)

	r.g.verd = func(_ workitem.Item, to string) verb.GateDecision {
		if to == "ready" {
			return verb.GateDecision{}
		}
		return acceptAll()
	}
	res = readyCall(t, epic.ID)[0]
	if res.Outcome != verb.ReadyMarked || !hasMark(t, epic.ID) {
		t.Fatalf("the re-run must mark the epic: %+v", res)
	}
	wantAdvice(t, "the re-run that marks the epic", res.Advise, false)
}

// The mark is a display tag: stripping it leaves the ledger row, so a later epic
// is still not the first.
func TestSecondEpicAfterTheFirstMarkIsStrippedCarriesNoAdvice(t *testing.T) {
	advisedRig(t, true)
	first, _ := advisedEpic(t, "a")
	wantAdvice(t, "first epic", readyCall(t, first.ID)[0].Advise, true)
	call(t, "story-set", map[string]any{"id": first.ID, "remove_tags": []string{"ready"}})
	if hasMark(t, first.ID) {
		t.Fatal("the first epic's mark must have been stripped")
	}

	second, _ := advisedEpic(t, "b")
	wantAdvice(t, "a second epic after the first mark was stripped", readyCall(t, second.ID)[0].Advise, false)
}

func TestStorySetFirstEpicReadinessCarriesTheAdvice(t *testing.T) {
	advisedRig(t, true)
	first, _ := advisedEpic(t, "a")
	wantAdvice(t, "story set on the first epic", setReadyAdvice(t, first.ID), true)

	second, _ := advisedEpic(t, "b")
	wantAdvice(t, "story ready on a second epic after story set", readyCall(t, second.ID)[0].Advise, false)
}

func TestStorySetAfterAStoryReadyEpicCarriesNoAdvice(t *testing.T) {
	advisedRig(t, true)
	first, _ := advisedEpic(t, "a")
	wantAdvice(t, "story ready on the first epic", readyCall(t, first.ID)[0].Advise, true)

	second, _ := advisedEpic(t, "b")
	wantAdvice(t, "story set on a second epic", setReadyAdvice(t, second.ID), false)
}

// A reused readiness round runs nothing, so it advises nothing — even when its
// step declares an advisor and no epic readiness has been accepted yet — and it
// leaves the repo's first epic round to advise. Only a story's readiness is
// reused; an epic's own ready step always runs, so presenting it after an
// accepted round is a fresh round that is not the first.
func TestReusedReadinessCarriesNoAdvice(t *testing.T) {
	adviseLine := `advise = { agent = "orchestrator", skill = "` + advisedSkill + `" }` + "\n"
	bothAdvised := strings.Replace(advisedStepToml(true), "reject_budget = 3\n", "reject_budget = 3\n"+adviseLine, 1)

	t.Run("story ready on a holding story readiness", func(t *testing.T) {
		advisedRigFor(t, bothAdvised)
		a := newStoryOf(t, "feature", "a")
		wantAdvice(t, "a story's accepted round", readyCall(t, a.ID)[0].Advise, false)
		res := readyCall(t, a.ID)[0]
		if res.Outcome != verb.ReadyMarked || !res.Reused {
			t.Fatalf("the second story ready must reuse the held readiness: %+v", res)
		}
		wantAdvice(t, "a reused story round", res.Advise, false)

		epic, _ := advisedEpic(t, "a")
		wantAdvice(t, "the first epic after a reused story round", readyCall(t, epic.ID)[0].Advise, true)
	})

	t.Run("story set presenting a holding story readiness edge", func(t *testing.T) {
		advisedRigFor(t, bothAdvised)
		a := newStoryOf(t, "feature", "a")
		first := readyCall(t, a.ID)[0]
		raw, err := dispatchRaw(t, "story-set", map[string]any{"id": a.ID, "status": "plan"})
		if err != nil {
			t.Fatalf("presenting the readiness edge: %v", err)
		}
		reuse := ledgerRows(t, a.ID, ledger.KindReadinessReuse)
		if len(reuse) != 1 || attemptOf(t, reuse[0]) != first.Attempt {
			t.Fatalf("the presented edge must reuse attempt %s: %+v", first.Attempt, reuse)
		}
		if strings.Contains(string(raw), `"advise"`) {
			t.Errorf("a reused readiness edge carries no advice: %s", raw)
		}

		epic, _ := advisedEpic(t, "a")
		wantAdvice(t, "the first epic after a reused story-set edge", readyCall(t, epic.ID)[0].Advise, true)
	})

	t.Run("story set on an epic readied by story ready", func(t *testing.T) {
		advisedRig(t, true)
		epic, _ := advisedEpic(t, "a")
		wantAdvice(t, "story ready on the first epic", readyCall(t, epic.ID)[0].Advise, true)
		wantAdvice(t, "story set on the same epic", setReadyAdvice(t, epic.ID), false)
	})
}

// A story-set response without advice is the record alone, as before.
func TestStorySetWithoutAdviceIsTheRecordAlone(t *testing.T) {
	advisedRig(t, false)
	epic, _ := advisedEpic(t, "a")
	raw, err := dispatchRaw(t, "story-set", map[string]any{"id": epic.ID, "status": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"advise"`) {
		t.Errorf("no advice is declared, so none is in the response: %s", raw)
	}
}

// The shipped [ready] step declares the advisor, so a repo with no workflow
// documents is advised on its first epic readiness by either command.
func TestShippedRouteFirstEpicReadinessAdvises(t *testing.T) {
	t.Run("story ready", func(t *testing.T) {
		wireShippedRoute(t)
		epic, _ := shippedEpicWithChild(t)
		res := readyCall(t, epic.ID)[0]
		if res.Outcome != verb.ReadyMarked {
			t.Fatalf("the shipped epic must be readied: %+v", res)
		}
		wantAdvice(t, "shipped route, story ready", res.Advise, true)
	})
	t.Run("story set", func(t *testing.T) {
		wireShippedRoute(t)
		epic, _ := shippedEpicWithChild(t)
		wantAdvice(t, "shipped route, story set", setReadyAdvice(t, epic.ID), true)
	})
}
