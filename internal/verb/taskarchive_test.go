package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// wireTasks opens a temp store, wires it into the verb registry, and points the
// task materialiser at a temp dir — returning the store and taskDir so a test can
// drive SyncTasks and inspect on-disk files.
func wireTasks(t *testing.T) (*workitem.Store, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	taskDir := filepath.Join(dir, ".satelle", "tasks")
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetTaskDir(taskDir)
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetTaskDir("")
	})
	return db.Stories, taskDir
}

// TestTaskArchiveDisposesRecordAndFiles proves the archive verb (sty_cd209b8a):
// the record is marked archived (excluded from the default list, still returned
// by get), the header + executions are MOVED to a timestamped backup (never
// deleted in place), and a subsequent SyncTasks never resurrects the record.
func TestTaskArchiveDisposesRecordAndFiles(t *testing.T) {
	st, taskDir := wireTasks(t)
	ctx := context.Background()

	var task workitem.Item
	json.Unmarshal(call(t, "task-create", map[string]any{"title": "Archive me"}), &task)
	header := filepath.Join(taskDir, task.ID+".md")
	if _, err := os.Stat(header); err != nil {
		t.Fatalf("task header not materialised: %v", err)
	}
	// A DONE (terminal) execution beside it — must not block archival, and must be
	// moved to the backup too.
	var exe workitem.Item
	json.Unmarshal(call(t, "execution-create", map[string]any{"title": "run", "parent_id": task.ID, "status": "done"}), &exe)

	// Archive.
	var archived workitem.Item
	json.Unmarshal(call(t, "task-archive", map[string]any{"id": task.ID}), &archived)
	if !archived.Archived {
		t.Error("archive verb did not return an archived record")
	}

	// Excluded from the default task list…
	var listed []workitem.Item
	json.Unmarshal(call(t, "task-list", nil), &listed)
	for _, it := range listed {
		if it.ID == task.ID {
			t.Error("archived task still appears in the default task list")
		}
	}
	// …but still readable via get, with its lineage.
	var got workitem.Item
	json.Unmarshal(call(t, "task-get", map[string]any{"id": task.ID}), &got)
	if got.ID != task.ID || !got.Archived {
		t.Errorf("task get did not return the archived record: %+v", got)
	}

	// Files moved to the backup, not left in place.
	if _, err := os.Stat(header); !os.IsNotExist(err) {
		t.Error("archived header was not removed from .satelle/tasks/")
	}
	if _, err := os.Stat(filepath.Join(taskDir, task.ID)); !os.IsNotExist(err) {
		t.Error("archived executions folder was not removed from .satelle/tasks/")
	}
	backups := filepath.Join(filepath.Dir(taskDir), "backups", "tasks")
	found := findFile(t, backups, task.ID+".md")
	if found == "" {
		t.Errorf("archived header not found under the backup dir %s", backups)
	}
	if findFile(t, backups, exe.ID+".md") == "" {
		t.Error("archived execution run not found under the backup dir")
	}

	// SyncTasks must NOT resurrect the archived record to a file.
	if _, _, err := verb.SyncTasks(ctx, st, time.Now()); err != nil {
		t.Fatalf("SyncTasks: %v", err)
	}
	if _, err := os.Stat(header); !os.IsNotExist(err) {
		t.Error("SyncTasks resurrected the archived task header")
	}
	if again, err := st.Get(ctx, task.ID); err != nil || !again.Archived {
		t.Errorf("archived flag lost after SyncTasks: %+v err=%v", again, err)
	}
}

// TestTaskArchiveRefusesLiveExecution proves archive refuses while a task has a
// non-terminal execution (sty_cd209b8a).
func TestTaskArchiveRefusesLiveExecution(t *testing.T) {
	wireTasks(t)

	var task workitem.Item
	json.Unmarshal(call(t, "task-create", map[string]any{"title": "Busy"}), &task)
	call(t, "execution-create", map[string]any{"title": "run", "parent_id": task.ID, "status": "in_progress"})

	_, err := verb.Dispatch(context.Background(), "task-archive", mustJSON(t, map[string]any{"id": task.ID}))
	if err == nil {
		t.Fatal("archive must refuse a task with a non-terminal execution")
	}
	if !strings.Contains(err.Error(), "non-terminal execution") {
		t.Errorf("refusal should name the live execution: %v", err)
	}
}

// findFile walks root looking for a file with the given base name, returning its
// path or "".
func findFile(t *testing.T, root, name string) string {
	t.Helper()
	var found string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && info.Name() == name {
			found = p
		}
		return nil
	})
	return found
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
