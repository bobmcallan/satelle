package verb

import (
	"context"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// refuseSkippedStep refuses a status change that is not a declared DOT edge of
// the item's governing workflow (sty_ebd3d666). Agents must walk declared edges
// (including recovery/park/cancel when the graph names them) — skipping a
// required step (e.g. backlog→in_progress when only backlog→plan exists) is a
// hard refuse naming the expected successor(s).
//
// Fail-open when the governing workflow cannot be resolved or has no DOT: those
// deployments are owned by structure/engage checks, not this fence. Stories and
// tasks with a resolvable DOT are fenced; executions follow their own paths.
func refuseSkippedStep(ctx context.Context, current workitem.Item, toStatus string) error {
	if current.Kind != workitem.KindStory && current.Kind != workitem.KindTask {
		return nil
	}
	from := current.Status
	if from == toStatus {
		return nil
	}
	idx, err := requireDocIndex()
	if err != nil {
		return nil
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return nil
	}
	wf, ok := wfgovern.GoverningWorkflow(wfs, current)
	if !ok {
		return nil
	}
	spec, ok := wfdot.Parse(wf.Body)
	if !ok {
		return nil
	}
	if spec.HasEdge(from, toStatus) {
		return nil
	}
	// Unknown from (legacy status not in graph): still refuse if to is not a
	// free-form — name successors of from when any, else generic.
	next := spec.Successors(from)
	if len(next) == 0 {
		return fmt.Errorf(
			"satelle: refusing transition %s→%s on %s — not an edge in workflow %s (no declared successors from %s); open a session on a legal path or fix the workflow DOT",
			from, toStatus, current.ID, wf.Name, from)
	}
	return fmt.Errorf(
		"satelle: refusing transition %s→%s on %s — not an edge in workflow %s; expected next step(s): %s",
		from, toStatus, current.ID, wf.Name, strings.Join(next, ", "))
}
