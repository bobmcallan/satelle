package storyfold

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// archivedDrift is the drift on the archived field alone.
func archivedDrift(row workitem.Item, st State) []Drift {
	var out []Drift
	for _, d := range Compare(row, st) {
		if d.Field == FieldArchived {
			out = append(out, d)
		}
	}
	return out
}

func archivedEvent(old, new string) ledger.Entry {
	return ev(ledger.KindStoryUpdated, map[string]any{"fields": []FieldChange{{Field: FieldArchived, Old: old, New: new}}})
}

// sty_28665836 AC5: archived is a folded field. A chain that never archived the
// story leaves it undefined, so the row's value stands; an archive and an
// unarchive set it, and a row that disagrees is a drift the overlay corrects.
func TestFoldCarriesArchived(t *testing.T) {
	st, _ := Fold(chain(created()), testRoute)
	if st.Defined[FieldArchived] {
		t.Error("a chain with no archive event defines archived")
	}
	row := workitem.Item{ID: "sty_a", Archived: true}
	if d := archivedDrift(row, st); len(d) != 0 {
		t.Errorf("an undefined archived drifted: %+v", d)
	}
	if got := Overlay(row, st); !got.Archived {
		t.Error("an undefined archived blanked the row's value")
	}

	st, _ = Fold(chain(created(), archivedEvent("false", "true")), testRoute)
	if !st.Defined[FieldArchived] || !st.Archived {
		t.Fatalf("archive event folded to %+v, want archived", st)
	}
	if d := archivedDrift(workitem.Item{ID: "sty_a"}, st); len(d) != 1 || d[0].Chain != "true" {
		t.Errorf("drift = %+v, want the archived field naming the chain's true", d)
	}
	if !Overlay(workitem.Item{}, st).Archived {
		t.Error("overlay did not archive the row")
	}

	st, _ = Fold(chain(created(), archivedEvent("false", "true"), archivedEvent("true", "false")), testRoute)
	if st.Archived || !st.Defined[FieldArchived] {
		t.Errorf("unarchive folded to %+v, want defined and not archived", st)
	}
	if st.Status != "backlog" {
		t.Errorf("archive moved the status to %q", st.Status)
	}
}
