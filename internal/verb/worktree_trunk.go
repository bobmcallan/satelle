package verb

import (
	"context"
	"fmt"
	"time"

	"github.com/bobmcallan/satelle/internal/trunk"
)

// cutBase decides what a worktree is cut from (sty_92337a13). When the repo has
// a remote and the [trunk] check is on, a base that names the trunk is first
// brought level with the remote — the cut then starts from main as other
// machines left it, not from a stale local copy. A base that is not the trunk (a
// dependency or epic branch) is cut as it was named, with no fetch.
//
// A state in [trunk] base_refuse stops the cut before anything is created. A
// trunk that cannot be named stops every cut, because satelle cannot tell it
// from a dependency branch. A state the repo lists out of base_refuse proceeds
// with the report line printed.
//
// It returns the ref to cut from and the report, which is nil when no check ran.
func cutBase(ctx context.Context, main, id, base, hint string) (string, *trunk.Report, error) {
	if !trunkCfg.Enabled() || !trunk.HasRemote(ctx, main, "") {
		return base, nil, nil
	}
	if hint == "" {
		hint = trunkCfg.Branch
	}
	opts := trunk.Options{Branch: hint, FastForward: true, ResolveHead: true}
	class := trunk.ClassifyBase(ctx, main, base, opts)
	var rep trunk.Report
	switch {
	case class.Unresolved || class.Offline:
		rep = class.Stop
	case class.IsTrunk:
		rep = trunk.Check(ctx, main, opts)
	default:
		return base, nil, nil
	}
	if !rep.Quiet() || rep.Unresolved {
		fmt.Fprintln(trunkOut, rep.Line())
		recordTrunkCheck(ctx, id, rep, time.Now())
	}
	if state := trunk.StopState(rep); state != "" && trunkCfg.BaseRefuses(state) {
		if rep.Unresolved {
			return "", &rep, fmt.Errorf("satelle: trunk unresolved — cannot tell whether %s is the trunk; %s", base, rep.Hint())
		}
		return "", &rep, fmt.Errorf("verb: worktree refused: trunk %s — %s", rep.Detail(), rep.Hint())
	}
	// Cut from the trunk as it now stands. A trunk that could not be named or
	// reached, let through by the repo's override, leaves the base as named.
	if class.IsTrunk && rep.State != trunk.Skipped {
		return "refs/heads/" + rep.Trunk, &rep, nil
	}
	return base, &rep, nil
}
