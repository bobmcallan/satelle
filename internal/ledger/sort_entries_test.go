package ledger

import (
	"testing"
	"time"
)

// SortEntries is List's order for rows read from anywhere else: oldest first, and
// by id for rows stamped at the same instant.
func TestSortEntriesOrdersByTimeThenID(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	es := []Entry{
		{ID: "evt_c", CreatedAt: t0.Add(time.Hour)},
		{ID: "evt_b", CreatedAt: t0},
		{ID: "evt_a", CreatedAt: t0},
		{ID: "evt_d", CreatedAt: t0.Add(-time.Hour)},
	}
	SortEntries(es)
	var got []string
	for _, e := range es {
		got = append(got, e.ID)
	}
	if want := []string{"evt_d", "evt_a", "evt_b", "evt_c"}; len(got) != 4 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Errorf("SortEntries order = %v, want %v", got, want)
	}
}
