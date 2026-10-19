package remotetest

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// The route overlays the binary's baseline gates on a remote's own (scoped to
// in_progress and done). The remote carries none of their skills, so the engine
// skips them as advisory; the pre-flight must not demand a reviewer binding for
// them either, or a remote with no reviewer on an edge could not be tested.
func TestPreflightIgnoresBaselineGatesTheRemoteDoesNotCarry(t *testing.T) {
	r := newRig(t)
	o := r.opts()
	var reviewerAsks int
	inner := o.NewRunner
	o.NewRunner = func(iface, command string) (agentcli.Runner, error) {
		if strings.Contains(command, "fake-reviewer") {
			reviewerAsks++
		}
		return inner(iface, command)
	}
	rep, err := Run(context.Background(), "edge", r.remote, r.item, o)
	if err != nil {
		t.Fatal(err)
	}
	if reviewerAsks != 1 {
		t.Errorf("the reviewer binding was resolved %d times; only plan→in_progress names a reviewer", reviewerAsks)
	}
	if last := rep.Steps[len(rep.Steps)-1]; last.To != "done" || last.Verdict != VerdictNoOp {
		t.Errorf("an edge whose only gates are baseline ones the remote lacks is a no-op, got %+v", last)
	}
}
