package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	Register(&Verb{
		Name:        "task-archive",
		Description: "Archive a task: mark the record archived (excluded from list) and move its files to a mandatory backup",
		Invoke:      taskArchive,
	})
}

// terminalExecutionStates are the run lifecycle states that count as finished for
// archiving purposes — a task with any non-terminal execution is still live and
// refuses archival (sty_cd209b8a).
var terminalExecutionStates = map[string]bool{
	workitem.StatusDone: true,
	"cancelled":         true,
}

// taskArchive disposes of a task HEADER (sty_cd209b8a): archive is a terminal
// bookkeeping DISPOSITION orthogonal to workflow status (status is a property of
// the operating workflow; archive is record disposition — hence a verb, not a
// status). It marks the store record archived (excluded from the default list,
// still returned by task get with its lineage), then MOVES the header .md and the
// executions folder to .satelle/backups/tasks/<ts>/<id>/ (mandatory backup,
// mirroring rebase — never deleting in place). It is UNGATED bookkeeping (no
// story-workflow traversal for the header) but REFUSES while the task has a
// non-terminal execution.
func taskArchive(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req idReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, fmt.Errorf("verb: id required")
	}
	it, err := store.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if it.Kind != workitem.KindTask {
		return nil, fmt.Errorf("verb: archive is task-only — %s is a %s", req.ID, it.Kind)
	}

	// Refuse while a run is still live: archiving a task with an in-flight
	// execution would strand the run's evidence.
	execs, err := store.List(ctx, workitem.ListFilter{
		Kind:            workitem.KindExecution,
		ParentID:        req.ID,
		IncludeArchived: true,
	})
	if err != nil {
		return nil, err
	}
	for _, e := range execs {
		if !terminalExecutionStates[e.Status] {
			return nil, fmt.Errorf("verb: task %s has a non-terminal execution %s (%s) — finish or cancel it before archiving",
				req.ID, e.ID, e.Status)
		}
	}

	now := time.Now()
	// Mark archived FIRST so the store List that drives file adoption immediately
	// excludes it — the serve watcher can never re-adopt the record to a file mid-move.
	archived, err := store.Archive(ctx, req.ID, now)
	if err != nil {
		return nil, err
	}
	backup, err := archiveTaskFiles(req.ID, now)
	if err != nil {
		return nil, err
	}

	detail := "archived"
	if backup != "" {
		detail = "archived → " + backup
	}
	appendLedger(ctx, req.ID, ledger.KindTaskUpdated, detail, now)
	appendOpLog("task-archive", req.ID, detail, now)
	notifyChange(TopicTasks)
	return json.Marshal(archived)
}
