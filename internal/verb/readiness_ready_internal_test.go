package verb

import (
	"errors"
	"fmt"
	"testing"
)

// sty_4cbe00aa: only a route with no readiness step leaves a child out of the way
// of its epic's readiness; any other failed lookup is refused and holds it back.
func TestClassifyChildEdgeErr(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		outcome   string
		holdsEpic bool
	}{
		{"unresolved route", errors.New("cannot resolve the route that governs x"), ReadyRefused, true},
		{"sentinel", fmt.Errorf("the route of x %w: no step", errNoReadinessStep), ReadyNoStep, false},
		{"bare sentinel", errNoReadinessStep, ReadyNoStep, false},
	}
	for _, c := range cases {
		outcome, holds := classifyChildEdgeErr(c.err)
		if outcome != c.outcome || holds != c.holdsEpic {
			t.Errorf("%s: got (%s, %v), want (%s, %v)", c.name, outcome, holds, c.outcome, c.holdsEpic)
		}
	}
}
