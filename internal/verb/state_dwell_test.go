package verb_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// StateDwell is ComputeStoryCost's occupancy rule on its own: a state is held from
// the row that entered it to the transition out, summed across re-entries; a park
// counts as time in the park state; the final state never accrues.
func TestStateDwellSumsOccupancyPerState(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }
	move := func(n, h int, from, to string) ledger.Entry {
		p, _ := json.Marshal(map[string]string{"from": from, "to": to})
		return ledger.Entry{ID: fmt.Sprintf("evt_%d", n), Kind: ledger.KindStatusTransition, Payload: p, CreatedAt: at(h)}
	}
	rows := []ledger.Entry{
		{ID: "evt_0", Kind: ledger.KindStoryCreated, CreatedAt: t0},
		move(1, 1, "backlog", "plan"),
		move(2, 3, "plan", "in_progress"),
		move(3, 4, "in_progress", "blocked"),
		move(4, 6, "blocked", "in_progress"),
		move(5, 7, "in_progress", "done"),
	}
	got := verb.StateDwell(rows)
	want := map[string]time.Duration{
		"backlog":     time.Hour,
		"plan":        2 * time.Hour,
		"in_progress": 2 * time.Hour,
		"blocked":     2 * time.Hour,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("StateDwell = %v, want %v", got, want)
	}
	if _, ok := got["done"]; ok {
		t.Error("the final state must not accrue")
	}
}
