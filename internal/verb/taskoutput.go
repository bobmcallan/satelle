package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	Register(&Verb{
		Name:        "execution-record",
		Description: "Record a task execution's run output as an OKF document under its task folder",
		Invoke:      executionRecord,
	})
}

// okfRunOutputType is the OKF `type` carried by a run-output document.
const okfRunOutputType = "task-execution-output"

// runOutputFileName is the per-run output doc name under the parent task folder.
// The execution id is unique per run, so a new run never overwrites a prior run's
// output (sty_890b86cb). The `output-` prefix keeps it out of the exe_*.md
// execution ingest walked by SyncTasks.
func runOutputFileName(execID string) string { return "output-" + execID + ".md" }

// recordRunOutput writes a task execution's run OUTPUT as an OKF document under
// its parent task's folder (.satelle/tasks/<tsk_id>/output-<exe_id>.md), carrying
// OKF frontmatter (required `type` + the `generated: satelle` marker) so a run's
// evidence is discoverable per task rather than only in the central
// executor.log (sty_890b86cb). This is the SINGLE write seam both capture paths
// use — the DISPATCHED path (the named agent's stdout written through
// mechanically from the dispatch site) and the IN-LOOP path (the
// execution-record verb the orchestrator calls). No-op when taskDir is unset;
// idempotent for the same run.
func recordRunOutput(exec workitem.Item, output string, now time.Time) (string, error) {
	if taskDir == "" {
		return "", nil
	}
	if exec.Kind != workitem.KindExecution || exec.ParentID == "" {
		return "", fmt.Errorf("record: %s is not an execution with a parent task", exec.ID)
	}
	dir := execDir(exec.ParentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("record: run-output dir: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntype: %s\ngenerated: satelle\nexecution: %s\ntask: %s\ntimestamp: %s\n---\n\n# Run output — %s\n\n%s\n",
		okfRunOutputType, exec.ID, exec.ParentID, now.UTC().Format(time.RFC3339),
		exec.ID, strings.TrimRight(output, "\n"))
	path := filepath.Join(dir, runOutputFileName(exec.ID))
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("record: write run output: %w", err)
	}
	return path, nil
}

// executionRecordReq carries the execution id and the run output to record. The
// in-loop orchestrator calls this as its final act of the run (the task
// workflow's exit-gate rubric instructs it to).
type executionRecordReq struct {
	ID     string `json:"id"`
	Output string `json:"output"`
}

func executionRecord(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req executionRecordReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, fmt.Errorf("verb: id required")
	}
	exec, err := store.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	path, werr := recordRunOutput(exec, req.Output, now)
	if werr != nil {
		return nil, werr
	}
	appendLedger(ctx, req.ID, ledger.KindTaskUpdated, "recorded run output", now)
	appendOpLog("execution-record", req.ID, "run output → "+path, now)
	notifyChange(TopicTasks)
	return json.Marshal(map[string]string{"execution": req.ID, "path": path})
}
