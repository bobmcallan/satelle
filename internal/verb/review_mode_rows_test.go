package verb

import (
	"encoding/json"
	"testing"
)

// sty_93359e0f: the rows a verdict becomes carry its mode and coverage — on the
// verdict row, and on each seat of a panel row — and an invocation row carries
// the turns a seat reported, or nothing, never a zero.

func rowOf(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("row: %v\n%s", err, raw)
	}
	return m
}

func TestReviewerRowStoresModeAndCoverage(t *testing.T) {
	row := rowOf(t, reviewerPayload("plan", "in_progress", ReviewerVerdict{
		Skill: "gate", ReviewMode: "fail-fast", Coverage: &Coverage{StoppedAt: "AC2"},
	}, "att1"))
	cov, _ := row["coverage"].(map[string]any)
	if row["review_mode"] != "fail-fast" || cov["complete"] != false || cov["stopped_at"] != "AC2" {
		t.Errorf("row = %v", row)
	}

	plain := rowOf(t, reviewerPayload("plan", "in_progress", ReviewerVerdict{Skill: "gate", Accept: true}, "att1"))
	for _, key := range []string{"review_mode", "coverage"} {
		if _, ok := plain[key]; ok {
			t.Errorf("a verdict with no mode or coverage writes no %q: %v", key, plain)
		}
	}
}

func TestPanelRowStoresEachSeatsCoverage(t *testing.T) {
	row := rowOf(t, reviewerPayload("plan", "in_progress", ReviewerVerdict{
		Skill: "gate", ReviewMode: "fail-fast", Combine: "fold",
		Seats: []PanelSeat{
			{Seat: "one", Notes: "stopped", Coverage: &Coverage{StoppedAt: "AC2"}},
			{Seat: "two", Accept: true, Coverage: &Coverage{Complete: true}},
			{Seat: "three", Accept: true},
		},
	}, "att1"))
	seats, _ := row["seats"].([]any)
	if len(seats) != 3 {
		t.Fatalf("seats = %v", row["seats"])
	}
	one, two, three := seats[0].(map[string]any), seats[1].(map[string]any), seats[2].(map[string]any)
	if c, _ := one["coverage"].(map[string]any); c["stopped_at"] != "AC2" || c["complete"] != false {
		t.Errorf("seat one coverage = %v", one["coverage"])
	}
	if c, _ := two["coverage"].(map[string]any); c["complete"] != true {
		t.Errorf("seat two coverage = %v", two["coverage"])
	}
	if _, ok := three["coverage"]; ok {
		t.Errorf("a seat that stated no coverage stores none: %v", three)
	}
}

func TestPartialVerdictDerivation(t *testing.T) {
	stopped := &Coverage{StoppedAt: "AC2"}
	for name, tc := range map[string]struct {
		decision string
		row      priorVerdictRow
		want     bool
	}{
		"full reject":                       {"reject", priorVerdictRow{}, false},
		"fail-fast reject, no coverage":     {"reject", priorVerdictRow{ReviewMode: "fail-fast"}, true},
		"fail-fast reject, stopped":         {"reject", priorVerdictRow{ReviewMode: "fail-fast", Coverage: stopped}, true},
		"fail-fast reject, claims complete": {"reject", priorVerdictRow{ReviewMode: "fail-fast", Coverage: &Coverage{Complete: true}}, true},
		"fail-fast accept":                  {"accept", priorVerdictRow{ReviewMode: "fail-fast"}, false},
		"accept stating complete":           {"accept", priorVerdictRow{Coverage: &Coverage{Complete: true}}, false},
		"accept stating incomplete":         {"accept", priorVerdictRow{Coverage: stopped}, true},
		"panel with a partial seat":         {"reject", priorVerdictRow{Seats: []PanelSeat{{Seat: "a"}, {Seat: "b", Coverage: stopped}}}, true},
		"panel of complete seats":           {"reject", priorVerdictRow{Seats: []PanelSeat{{Seat: "a", Coverage: &Coverage{Complete: true}}}}, false},
	} {
		if got := partialVerdict(tc.decision, tc.row); got != tc.want {
			t.Errorf("%s: partialVerdict = %v, want %v", name, got, tc.want)
		}
	}
}
