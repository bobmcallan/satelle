package cli

// `satelle sync documents` — the CLI counterpart to the hosted workspace
// path-document store. push/pull are PERSONAL ONLY (epic:sync-publish): local
// .satelle/documents ↔ this repo's bound hosted project's personal collection.
// Team is not a sync destination — use satelle publish for the team catalog.

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/subsync"
)

// newSyncDocumentsCmd builds the `satelle sync documents` group.
func newSyncDocumentsCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "documents",
		Short: "Push/pull documents for this repo's bound hosted project (local default: no hosted write)",
	}

	var pushServer, pushWorkspace string
	var dryRun bool
	push := &cobra.Command{
		Use:   "push",
		Short: "Upload authored documents to the workspace store (a new version per file)",
		Long: `push walks the documents area per its resolved [sync] scope — skipping local —
and uploads each file as a new version into this repo's bound hosted PROJECT's
personal collection only (epic:sync-publish). Identical content is idempotent.
Team is not a sync destination; use satelle publish to expose documents to a team
catalog. Requires "satelle project bind <slug>".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncDocumentsPush(cmd, pushServer, pushWorkspace, dryRun)
		},
	}
	push.Flags().StringVar(&pushServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	push.Flags().StringVar(&pushWorkspace, "workspace", "", "Ignored for push (sync is personal-only; kept for flag compatibility).")
	push.Flags().BoolVar(&dryRun, "dry-run", false, "List what would be pushed without contacting the server.")
	group.AddCommand(push)

	var pullServer, pullWorkspace string
	pull := &cobra.Command{
		Use:   "pull",
		Short: "Pull document changes into this repo (incremental, byte-exact)",
		Long: `pull fetches documents that changed since the last cursor from this repo's
bound hosted PROJECT's personal collection only (epic:sync-publish) and writes
them byte-for-byte into this repo's documents area. Up-to-date trees report
"up to date". Requires "satelle project bind <slug>".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncDocumentsPull(cmd, pullServer, pullWorkspace)
		},
	}
	pull.Flags().StringVar(&pullServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	pull.Flags().StringVar(&pullWorkspace, "workspace", "", "Ignored for pull (sync is personal-only; kept for flag compatibility).")
	group.AddCommand(pull)

	return group
}

func runSyncDocumentsPush(cmd *cobra.Command, serverArg, workspaceArg string, dryRun bool) error {
	cfg, repoRoot, _, err := loadRepoConfig()
	if err != nil {
		return err
	}
	_ = workspaceArg // sync is personal-only (epic:sync-publish)
	server := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	files, scope, err := config.DocumentFiles(cfg, repoRoot)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if scope == config.LocalScope {
		fmt.Fprintln(out, "documents scope is local — skipping. Set [sync] documents = personal to opt in.")
		return nil
	}
	if len(files) == 0 {
		fmt.Fprintln(out, "No documents to push — documents area is empty.")
		return nil
	}
	if note := sharedSyncNote(files); note != "" {
		fmt.Fprintln(out, note)
	}
	if dryRun {
		fmt.Fprintf(out, "Would push %d document(s) to bound project personal collection on %s:\n", len(files), server)
		for _, f := range files {
			fmt.Fprintf(out, "  %-40s -> personal\n", f.Path)
		}
		return nil
	}
	// Bound project before any network (AC5).
	project, err := resolveBoundProject(cfg)
	if err != nil {
		return err
	}
	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	var created, skipped int
	for _, f := range files {
		res, perr := client.PushDocumentFile(cmd.Context(), project, f.Path, f.Content)
		if perr != nil {
			if errors.Is(perr, hosted.ErrLoginRequired) {
				return perr
			}
			if errors.Is(perr, hosted.ErrDocumentConflict) {
				return fmt.Errorf("push %s: conflict — resolve on the server and retry", f.Path)
			}
			return fmt.Errorf("push %s: %w", f.Path, perr)
		}
		if res.Created {
			created++
		} else {
			skipped++
		}
	}
	fmt.Fprintf(out, "Pushed %d document(s) to project %q personal collection on %s: %d new, %d unchanged.\n", len(files), project, server, created, skipped)
	return nil
}

func runSyncDocumentsPull(cmd *cobra.Command, serverArg, workspaceArg string) error {
	cfg, repoRoot, dataDir, err := loadRepoConfig()
	if err != nil {
		return err
	}
	_ = workspaceArg // sync is personal-only (epic:sync-publish)
	server := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	scope, err := config.ScopeFor(cfg, "documents")
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if scope == config.LocalScope {
		fmt.Fprintln(out, "documents scope is local — skipping. Set [sync] documents = personal to opt in.")
		return nil
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		absRoot = repoRoot
	}
	// Bound project before any network (AC5).
	project, err := resolveBoundProject(cfg)
	if err != nil {
		return err
	}
	client := hosted.NewClient(server, hosted.FileStore{}, nil)

	// Personal only (epic:sync-publish). Team catalog is via publish/adopt.
	// Project-addressed routes (sty_ca64d0cb) need no workspace id.
	written, skipped, perr := pullDocumentsFromProject(cmd, client, server, absRoot, dataDir, project, "personal")
	if perr != nil {
		return perr
	}
	if written == 0 && skipped == 0 {
		fmt.Fprintf(out, "Documents up to date on %s.\n", server)
		return nil
	}
	// AC4 (sty_84f14ace): skipped local-only paths must be visible — a skip-only
	// batch must not look identical to "nothing to pull" / "up to date".
	switch {
	case written > 0 && skipped > 0:
		fmt.Fprintf(out, "Pulled %d document(s) from project %q personal collection on %s into %s, %d skipped (local-only path).\n", written, project, server, dataDir, skipped)
	case written > 0:
		fmt.Fprintf(out, "Pulled %d document(s) from project %q personal collection on %s into %s.\n", written, project, server, dataDir)
	default:
		fmt.Fprintf(out, "Documents pull on %s: %d skipped (local-only path); cursor advanced.\n", server, skipped)
	}
	return nil
}

// pullDocumentsFromProject runs one project incremental pull: load cursor, list
// changes, fetch content, Restore, THEN save cursor (only after a successful
// restore so a crash re-fetches rather than silently drops files). Excluded
// (local-only) paths are filtered before fetch (sty_0fd04503) and still skipped
// by Restore as defence in depth, so a partition already poisoned with backups/
// can unwedge without hard-erroring (sty_84f14ace). Project-addressed routes
// (sty_ca64d0cb) key the cursor on (server, project, repo) only.
func pullDocumentsFromProject(cmd *cobra.Command, client *hosted.Client, server, absRoot, dataDir, project, label string) (written, skipped int, err error) {
	cursor, err := hosted.LoadDocumentCursor(server, project, absRoot)
	if err != nil {
		return 0, 0, fmt.Errorf("load document cursor (%s): %w", label, err)
	}
	changes, err := client.ListDocumentChanges(cmd.Context(), project, cursor)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return 0, 0, err
		}
		return 0, 0, fmt.Errorf("list document changes (%s): %w", label, err)
	}
	if len(changes.Items) == 0 {
		// Still advance the cursor when the server issues a new one with an empty batch.
		if changes.Cursor != "" && changes.Cursor != cursor {
			if err := hosted.SaveDocumentCursor(server, project, absRoot, changes.Cursor); err != nil {
				return 0, 0, fmt.Errorf("save document cursor (%s): %w", label, err)
			}
		}
		return 0, 0, nil
	}
	var files []subsync.File
	preSkipped := 0
	for _, item := range changes.Items {
		// Skip before fetch so a poisoned partition does not pay bandwidth for
		// bodies Restore would refuse (sty_0fd04503 AC1). Restore remains the
		// enforcement point for any path that still reaches it.
		if subsync.ExcludedLocal(item.Path) {
			preSkipped++
			continue
		}
		content, _, ferr := client.DocumentFileContent(cmd.Context(), project, item.Path)
		if ferr != nil {
			if errors.Is(ferr, hosted.ErrLoginRequired) {
				return 0, 0, ferr
			}
			if errors.Is(ferr, hosted.ErrDocumentFileMissing) {
				continue // listed then deleted — skip
			}
			return 0, 0, fmt.Errorf("fetch document %s (%s): %w", item.Path, label, ferr)
		}
		files = append(files, subsync.File{Path: item.Path, Content: content})
	}
	// Totalling sits outside if len(files)>0: an all-excluded batch has files==nil
	// and preSkipped>0; leaving skipped only inside the if would print "up to date"
	// and regress sty_84f14ace AC4.
	skipped = preSkipped
	if len(files) > 0 {
		res, rerr := subsync.Restore(dataDir, files)
		if rerr != nil {
			return 0, 0, fmt.Errorf("restore documents (%s): %w", label, rerr)
		}
		written = res.Written
		skipped += len(res.Skipped)
	}
	// Cursor advances after a successful restore — including skip-only batches —
	// so already-poisoned partitions unwedge on the next pull (sty_84f14ace AC2).
	if changes.Cursor != "" {
		if err := hosted.SaveDocumentCursor(server, project, absRoot, changes.Cursor); err != nil {
			return written, skipped, fmt.Errorf("save document cursor (%s): %w", label, err)
		}
	}
	return written, skipped, nil
}
