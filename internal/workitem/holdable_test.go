package workitem

import "testing"

// Executions are append-only and never held; the id form and the kind form agree.
func TestHoldable(t *testing.T) {
	for _, tc := range []struct {
		kind Kind
		id   string
		want bool
	}{
		{KindStory, "sty_abc12345", true},
		{KindTask, "tsk_abc12345", true},
		{KindExecution, "exe_abc12345", false},
	} {
		if got := tc.kind.Holdable(); got != tc.want {
			t.Errorf("%s.Holdable() = %v, want %v", tc.kind, got, tc.want)
		}
		if got := Holdable(tc.id); got != tc.want {
			t.Errorf("Holdable(%q) = %v, want %v", tc.id, got, tc.want)
		}
		if got := Holdable(tc.kind.newID()); got != tc.want {
			t.Errorf("Holdable(newID of %s) = %v, want %v", tc.kind, got, tc.want)
		}
	}
}
