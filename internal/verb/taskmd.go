package verb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
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

// execDir is the per-task folder holding a task's execution runs
// (<taskDir>/<tsk_id>/exe_<id>.md); created lazily on the first execution
// (sty_ef08ce2a). The task header itself stays a FLAT file beside the folder.
func execDir(taskID string) string { return filepath.Join(taskDir, taskID) }

// execFilePath returns the on-disk path of an execution's run file, under its
// parent task's folder. Empty when the execution has no parent task.
func execFilePath(it workitem.Item) string {
	if it.ParentID == "" {
		return ""
	}
	return filepath.Join(execDir(it.ParentID), it.ID+".md")
}

// writeItemFile materialises a task header (flat) or an execution run (under its
// parent task's folder) to its .md file — the source of truth. No-op when
// taskDir is unset or the item is neither a task nor an execution.
func writeItemFile(it workitem.Item) error {
	if taskDir == "" {
		return nil
	}
	switch it.Kind {
	case workitem.KindTask:
		if err := os.MkdirAll(taskDir, 0o755); err != nil {
			return fmt.Errorf("task file dir: %w", err)
		}
		return os.WriteFile(taskFilePath(it.ID), workitem.Marshal(it), 0o644)
	case workitem.KindExecution:
		path := execFilePath(it)
		if path == "" {
			return fmt.Errorf("execution %s has no parent task", it.ID)
		}
		if err := os.MkdirAll(execDir(it.ParentID), 0o755); err != nil {
			return fmt.Errorf("execution file dir: %w", err)
		}
		return os.WriteFile(path, workitem.Marshal(it), 0o644)
	default:
		return nil
	}
}

// archiveTaskFiles MOVES an archived task's on-disk evidence — its flat header
// (tsk_<id>.md) and its executions folder (tsk_<id>/) — into a mandatory
// timestamped backup at .satelle/backups/tasks/<ts>/<id>/, mirroring rebase's
// backup-not-delete pattern (sty_cd209b8a). Nothing is deleted in place. Returns
// the backup dir (empty when taskDir is unset). Idempotent-ish: missing sources
// are skipped.
func archiveTaskFiles(id string, now time.Time) (string, error) {
	if taskDir == "" {
		return "", nil
	}
	ts := now.UTC().Format("20060102-150405")
	root := resolveBackupsDir(taskDir)
	dst := filepath.Join(root, "tasks", ts, id)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", fmt.Errorf("archive backup dir: %w", err)
	}
	// Header file → dst/<id>.md.
	if hdr := taskFilePath(id); fileStat(hdr) {
		if err := os.Rename(hdr, filepath.Join(dst, id+".md")); err != nil {
			return "", fmt.Errorf("archive header %s: %w", id, err)
		}
	}
	// Executions folder contents → dst/ (each run beside the header); the emptied
	// folder is then removed so reindex never walks it again.
	ed := execDir(id)
	if entries, err := os.ReadDir(ed); err == nil {
		for _, e := range entries {
			if err := os.Rename(filepath.Join(ed, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return "", fmt.Errorf("archive execution %s: %w", e.Name(), err)
			}
		}
		_ = os.Remove(ed)
	}
	return dst, nil
}

// fileStat reports whether path exists (a small helper for the archive move).
func fileStat(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// SyncTasks reconciles the task files under taskDir with the store: any store
// task lacking a file is first ADOPTED by writing its file (migrating legacy
// DB-only tasks, e.g. tasks created before tasks became substrate), then every
// tsk_*.md is parsed and upserted so the FILE wins — it is the source of truth.
// Embedded default tasks (config.EmbeddedDefaults kind=tasks) with no on-disk
// file are ingested into the store WITHOUT writing a seed file (sty_29e5a9a5
// virtual sparse defaults). Archived tasks are excluded from the store List that
// drives adoption, so an archived record is never resurrected (sty_cd209b8a).
// Returns (indexed, migrated). A no-op when taskDir is unset or the store is nil.
func SyncTasks(ctx context.Context, store *workitem.Store, now time.Time) (indexed, migrated int, err error) {
	if taskDir == "" || store == nil {
		return 0, 0, nil
	}
	if err = os.MkdirAll(taskDir, 0o755); err != nil {
		return 0, 0, fmt.Errorf("task file dir: %w", err)
	}
	// Embedded task names that must not be re-written to disk by adopt when they
	// exist only as virtual defaults.
	virtualTaskIDs := map[string]struct{}{}
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "tasks" {
			virtualTaskIDs[d.Name] = struct{}{}
		}
	}
	// 1. Adopt store tasks/executions that have no file yet (legacy DB-only tasks;
	// executions created before their file was written). Skip pure virtual defaults.
	for _, kind := range []workitem.Kind{workitem.KindTask, workitem.KindExecution} {
		dbItems, lerr := store.List(ctx, workitem.ListFilter{Kind: kind})
		if lerr != nil {
			return indexed, migrated, lerr
		}
		for _, it := range dbItems {
			var path string
			switch kind {
			case workitem.KindTask:
				path = taskFilePath(it.ID)
				if _, virt := virtualTaskIDs[it.ID]; virt {
					continue // virtual default — never re-seed onto disk
				}
			case workitem.KindExecution:
				path = execFilePath(it)
			}
			if path == "" {
				continue
			}
			if _, serr := os.Stat(path); os.IsNotExist(serr) {
				if werr := writeItemFile(it); werr != nil {
					return indexed, migrated, werr
				}
				migrated++
			}
		}
	}
	// 2. Ingest every task header (flat tsk_*.md) AND every execution run
	// (tsk_*/exe_*.md, one per-task subfolder) into the store — the file is the
	// source of truth. A subdir named like a task id is walked for its runs.
	onDiskTasks := map[string]struct{}{}
	entries, derr := os.ReadDir(taskDir)
	if derr != nil {
		return indexed, migrated, derr
	}
	for _, ent := range entries {
		name := ent.Name()
		switch {
		case ent.IsDir() && strings.HasPrefix(name, "tsk_"):
			n, ierr := ingestExecutions(ctx, store, filepath.Join(taskDir, name), now)
			if ierr != nil {
				return indexed, migrated, ierr
			}
			indexed += n
		case !ent.IsDir() && strings.HasPrefix(name, "tsk_") && strings.HasSuffix(name, ".md"):
			onDiskTasks[strings.TrimSuffix(name, ".md")] = struct{}{}
			did, ierr := ingestItemFile(ctx, store, filepath.Join(taskDir, name), workitem.KindTask, now)
			if ierr != nil {
				return indexed, migrated, ierr
			}
			if did {
				indexed++
			}
		}
	}
	// 3. Virtual default tasks with no on-disk override: ingest body into the store
	// without writing a seed file (disk would win if present).
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind != "tasks" {
			continue
		}
		if _, onDisk := onDiskTasks[d.Name]; onDisk {
			continue
		}
		// Write to a temp path? ingestItemFile needs a path. Parse body directly.
		if did, ierr := ingestItemBody(ctx, store, d.Name, d.Body, workitem.KindTask, now); ierr != nil {
			return indexed, migrated, ierr
		} else if did {
			indexed++
		}
	}
	return indexed, migrated, nil
}

// ingestExecutions ingests every exe_*.md run file under a task's folder,
// stamping KindExecution and the parent task id (the folder name). Returns how
// many runs were (re)indexed.
func ingestExecutions(ctx context.Context, store *workitem.Store, dir string, now time.Time) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	parentID := filepath.Base(dir)
	n := 0
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasPrefix(name, "exe_") || !strings.HasSuffix(name, ".md") {
			continue
		}
		it, ok := parseItemFile(filepath.Join(dir, name), workitem.KindExecution, now)
		if !ok {
			continue
		}
		it.ParentID = parentID // the folder is the authority on parentage
		did, uerr := upsertIfChanged(ctx, store, it, now)
		if uerr != nil {
			return n, uerr
		}
		if did {
			n++
		}
	}
	return n, nil
}

// parseItemFile reads a work-item .md file, stamping the given kind and seeding
// CreatedAt when absent. ok is false when the file can't be read/parsed (skipped
// so one malformed file never aborts the whole sync).
func parseItemFile(path string, kind workitem.Kind, now time.Time) (workitem.Item, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return workitem.Item{}, false
	}
	it, perr := workitem.Parse(data)
	if perr != nil {
		return workitem.Item{}, false
	}
	it.Kind = kind
	if it.CreatedAt.IsZero() {
		it.CreatedAt = now
	}
	return it, true
}

// ingestItemFile parses one file and upserts it when its content differs from
// the store. Returns whether a row was (re)indexed. A parse failure is skipped
// (did=false, no error) so one malformed file never aborts the whole sync.
func ingestItemFile(ctx context.Context, store *workitem.Store, path string, kind workitem.Kind, now time.Time) (bool, error) {
	it, ok := parseItemFile(path, kind, now)
	if !ok {
		return false, nil
	}
	return upsertIfChanged(ctx, store, it, now)
}

// ingestItemBody parses an in-memory task body (virtual embedded default) and
// upserts it without writing a seed file to disk (sty_29e5a9a5).
func ingestItemBody(ctx context.Context, store *workitem.Store, name, body string, kind workitem.Kind, now time.Time) (bool, error) {
	it, perr := workitem.Parse([]byte(body))
	if perr != nil {
		return false, nil
	}
	it.Kind = kind
	if it.ID == "" {
		it.ID = name
	}
	if it.CreatedAt.IsZero() {
		it.CreatedAt = now
	}
	return upsertIfChanged(ctx, store, it, now)
}

// upsertIfChanged upserts an item only when its governed content differs from
// the stored row — skipping unchanged files so the serve watcher (which resyncs
// every couple of seconds) doesn't churn the store fingerprint and trigger
// constant web refetches.
func upsertIfChanged(ctx context.Context, store *workitem.Store, it workitem.Item, now time.Time) (bool, error) {
	if existing, gerr := store.Get(ctx, it.ID); gerr == nil && taskContentEqual(existing, it) {
		return false, nil
	}
	it.UpdatedAt = now
	if _, uerr := store.Upsert(ctx, it, now); uerr != nil {
		return false, uerr
	}
	return true, nil
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
