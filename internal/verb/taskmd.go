package verb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// taskDir is the per-repo dir holding task work-definition files
// (<taskDir>/tsk_<id>.md). Unlike stories — whose per-item .md mirror was
// removed so the DB is the sole store (sty_fa1e02e1) — a task is AUTHORED
// SUBSTRATE (sty_c1f9e74c): the FILE is the source of truth and the store is
// merely its index, so a task can be authored/edited on disk and ingested. This
// is NOT the removed DB->disk mirror (there the DB was primary); here the file
// is primary. Empty disables materialisation (tests that don't opt in).
var taskDir string

// SetTaskDir wires the directory that holds task work-definition files.
func SetTaskDir(dir string) { taskDir = dir }

func taskFilePath(id string) string { return filepath.Join(taskDir, id+".md") }

// writeTaskFile materialises a task to its .md work-definition file (the source
// of truth). No-op when taskDir is unset or the item is not a task.
func writeTaskFile(it workitem.Item) error {
	if taskDir == "" || it.Kind != workitem.KindTask {
		return nil
	}
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return fmt.Errorf("task file dir: %w", err)
	}
	return os.WriteFile(taskFilePath(it.ID), workitem.Marshal(it), 0o644)
}

// SyncTasks reconciles the task files under taskDir with the store: any store
// task lacking a file is first ADOPTED by writing its file (migrating legacy
// DB-only tasks, e.g. tasks created before tasks became substrate), then every
// tsk_*.md is parsed and upserted so the FILE wins — it is the source of truth.
// Returns (indexed, migrated). A no-op when taskDir is unset or the store is nil.
func SyncTasks(ctx context.Context, store *workitem.Store, now time.Time) (indexed, migrated int, err error) {
	if taskDir == "" || store == nil {
		return 0, 0, nil
	}
	if err = os.MkdirAll(taskDir, 0o755); err != nil {
		return 0, 0, fmt.Errorf("task file dir: %w", err)
	}
	// 1. Adopt store tasks that have no file yet (legacy DB-only tasks).
	dbTasks, lerr := store.List(ctx, workitem.ListFilter{Kind: workitem.KindTask})
	if lerr != nil {
		return 0, 0, lerr
	}
	for _, it := range dbTasks {
		if _, serr := os.Stat(taskFilePath(it.ID)); os.IsNotExist(serr) {
			if werr := writeTaskFile(it); werr != nil {
				return indexed, migrated, werr
			}
			migrated++
		}
	}
	// 2. Ingest every task file into the store (file is source of truth).
	entries, derr := os.ReadDir(taskDir)
	if derr != nil {
		return indexed, migrated, derr
	}
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasPrefix(name, "tsk_") || !strings.HasSuffix(name, ".md") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(taskDir, name))
		if rerr != nil {
			return indexed, migrated, rerr
		}
		it, perr := workitem.Parse(data)
		if perr != nil {
			// A malformed file must not abort the whole sync — skip it; `satelle
			// validate` surfaces its structure problems separately.
			continue
		}
		it.Kind = workitem.KindTask
		if it.CreatedAt.IsZero() {
			it.CreatedAt = now
		}
		// Skip files whose content already matches the store: the continuous serve
		// watcher calls this every couple of seconds, so re-upserting unchanged rows
		// would churn the store fingerprint and trigger constant web refetches.
		if existing, gerr := store.Get(ctx, it.ID); gerr == nil && taskContentEqual(existing, it) {
			continue
		}
		it.UpdatedAt = now
		if _, uerr := store.Upsert(ctx, it, now); uerr != nil {
			return indexed, migrated, uerr
		}
		indexed++
	}
	return indexed, migrated, nil
}

// taskContentEqual reports whether two tasks carry the same governed content,
// ignoring timestamps — the comparison SyncTasks uses to decide a file is
// unchanged and skip a needless upsert.
func taskContentEqual(a, b workitem.Item) bool {
	return a.Title == b.Title && a.Body == b.Body && a.Status == b.Status &&
		a.Priority == b.Priority && a.Category == b.Category &&
		a.ParentID == b.ParentID && a.AcceptanceCriteria == b.AcceptanceCriteria &&
		strings.Join(a.Tags, ",") == strings.Join(b.Tags, ",")
}
