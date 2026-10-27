package verb

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// A child with no resolvable route is held with the machine-readable no_route
// cause, so a script need not read the prose Reason.
func TestComputeWaveNoRouteCause(t *testing.T) {
	kid := workitem.Item{ID: "sty_aaaa0001", Status: "backlog"}
	runnable, omitted, err := computeWave("parallel", []workitem.Item{kid}, func(string) (waveState, bool) {
		return waveState{Status: "backlog", Known: false}, true
	})
	if err != nil {
		t.Fatalf("computeWave: %v", err)
	}
	if len(runnable) != 0 {
		t.Errorf("a child with no route must not be runnable: %v", runnable)
	}
	if len(omitted) != 1 || omitted[0].ID != kid.ID || omitted[0].Cause != WaveCauseNoRoute || len(omitted[0].WaitingOn) != 0 {
		t.Errorf("omitted = %+v, want %s held with cause %q", omitted, kid.ID, WaveCauseNoRoute)
	}
}
