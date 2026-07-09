package cli

// `satelle sync documents` — the CLI counterpart to the hosted workspace
// document store (epic:scoped-sync, order:6). push uploads authored documents
// per their RESOLVED [sync] scope (skipping local, honoring the per-file shared
// flag) as new per-file versions; pull fetches incremental changes via the
// server's changed-since surface and materializes them byte-for-byte into this
// repo. Gemini/document conversion is deferred — content is authored markdown.

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
		Short: "Push authored documents to / pull them from the hosted workspace document store",
	}

	var pushServer, pushWorkspace string
	var dryRun bool
	push := &cobra.Command{
		Use:   "push",
		Short: "Upload authored documents to the workspace store (a new version per file)",
		Long: `push walks the documents area per its resolved [sync] scope — skipping local
and honoring the per-file shared flag — and uploads each file as a new version
into the destination workspace. Identical content is idempotent (no new version).
Shared-tier files route to the team workspace; the rest to your personal workspace.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncDocumentsPush(cmd, pushServer, pushWorkspace, dryRun)
		},
	}
	push.Flags().StringVar(&pushServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	push.Flags().StringVar(&pushWorkspace, "workspace", "", "Team-workspace name shared-tier files route to (overrides the active workspace).")
	push.Flags().BoolVar(&dryRun, "dry-run", false, "List what would be pushed and the destination tier, without contacting the server.")
	group.AddCommand(push)

	var pullServer, pullWorkspace string
	pull := &cobra.Command{
		Use:   "pull",
		Short: "Pull document changes into this repo (incremental, byte-exact)",
		Long: `pull fetches documents that changed since the last cursor for each destination
workspace (personal always; team when a team workspace is bound) and writes them
byte-for-byte into this repo's documents area. Up-to-date trees report "up to date".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncDocumentsPull(cmd, pullServer, pullWorkspace)
		},
	}
	pull.Flags().StringVar(&pullServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	pull.Flags().StringVar(&pullWorkspace, "workspace", "", "Team workspace to also pull from (overrides the active workspace).")
	group.AddCommand(pull)

	return group
}

func runSyncDocumentsPush(cmd *cobra.Command, serverArg, workspaceArg string, dryRun bool) error {
	cfg, repoRoot, _, err := loadRepoConfig()
	if err != nil {
		return err
	}
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
		fmt.Fprintln(out, "documents scope is local — skipping. Set [sync] documents = personal|shared to opt in.")
		return nil
	}
	if len(files) == 0 {
		fmt.Fprintln(out, "No documents to push — documents area is empty.")
		return nil
	}
	teamName := resolveTeamWorkspaceName(cfg, workspaceArg)
	if dryRun {
		fmt.Fprintf(out, "Would push %d document(s) to %s:\n", len(files), server)
		for _, f := range files {
			dest := "your personal workspace"
			if f.Tier == config.SharedTier {
				if teamName == "" {
					dest = "(no team workspace selected)"
				} else {
					dest = fmt.Sprintf("team workspace %q", teamName)
				}
			}
			fmt.Fprintf(out, "  [%s] %-40s -> %s\n", f.Tier, f.Path, dest)
		}
		return nil
	}
	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	personalID, err := client.ActiveWorkspaceID(cmd.Context(), config.PersonalWorkspace)
	if err != nil {
		return fmt.Errorf("resolve personal workspace: %w", err)
	}
	var teamID string
	if teamName != "" {
		if teamID, err = client.ActiveWorkspaceID(cmd.Context(), teamName); err != nil {
			return fmt.Errorf("resolve team workspace: %w", err)
		}
	}
	var created, skipped int
	for _, f := range files {
		wsID := personalID
		if f.Tier == config.SharedTier {
			if teamID == "" {
				return fmt.Errorf("shared document %q has no team workspace — run \"satelle login --workspace <team>\" or pass --workspace to select one", f.Path)
			}
			wsID = teamID
		}
		res, perr := client.PushDocumentFile(cmd.Context(), wsID, f.Path, f.Content)
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
	fmt.Fprintf(out, "Pushed %d document(s) to %s: %d new, %d unchanged.\n", len(files), server, created, skipped)
	return nil
}

func runSyncDocumentsPull(cmd *cobra.Command, serverArg, workspaceArg string) error {
	cfg, repoRoot, dataDir, err := loadRepoConfig()
	if err != nil {
		return err
	}
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
		fmt.Fprintln(out, "documents scope is local — skipping. Set [sync] documents = personal|shared to opt in.")
		return nil
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		absRoot = repoRoot
	}
	client := hosted.NewClient(server, hosted.FileStore{}, nil)

	// Always pull personal; also pull team when a team workspace is selected —
	// push splits one documents area across tiers per-file, so reconstructing
	// the tree needs both sources (plan decision 2).
	personalID, err := client.ActiveWorkspaceID(cmd.Context(), config.PersonalWorkspace)
	if err != nil {
		return fmt.Errorf("resolve personal workspace: %w", err)
	}
	sources := []struct {
		label string
		wsID  string
	}{{label: "personal", wsID: personalID}}
	if teamName := resolveTeamWorkspaceName(cfg, workspaceArg); teamName != "" {
		teamID, terr := client.ActiveWorkspaceID(cmd.Context(), teamName)
		if terr != nil {
			return fmt.Errorf("resolve team workspace: %w", terr)
		}
		sources = append(sources, struct {
			label string
			wsID  string
		}{label: teamName, wsID: teamID})
	}

	var total int
	for _, src := range sources {
		n, perr := pullDocumentsFromWorkspace(cmd, client, server, absRoot, dataDir, src.wsID, src.label)
		if perr != nil {
			return perr
		}
		total += n
	}
	if total == 0 {
		fmt.Fprintf(out, "Documents up to date on %s.\n", server)
		return nil
	}
	fmt.Fprintf(out, "Pulled %d document(s) from %s into %s.\n", total, server, dataDir)
	return nil
}

// pullDocumentsFromWorkspace runs one workspace's incremental pull: load cursor,
// list changes, fetch content, Restore, THEN save cursor (only after a successful
// restore so a crash re-fetches rather than silently drops files).
func pullDocumentsFromWorkspace(cmd *cobra.Command, client *hosted.Client, server, absRoot, dataDir, wsID, label string) (int, error) {
	cursor, err := hosted.LoadDocumentCursor(server, wsID, absRoot)
	if err != nil {
		return 0, fmt.Errorf("load document cursor (%s): %w", label, err)
	}
	changes, err := client.ListDocumentChanges(cmd.Context(), wsID, cursor)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return 0, err
		}
		return 0, fmt.Errorf("list document changes (%s): %w", label, err)
	}
	if len(changes.Items) == 0 {
		// Still advance the cursor when the server issues a new one with an empty batch.
		if changes.Cursor != "" && changes.Cursor != cursor {
			if err := hosted.SaveDocumentCursor(server, wsID, absRoot, changes.Cursor); err != nil {
				return 0, fmt.Errorf("save document cursor (%s): %w", label, err)
			}
		}
		return 0, nil
	}
	var files []subsync.File
	for _, item := range changes.Items {
		content, _, ferr := client.DocumentFileContent(cmd.Context(), wsID, item.Path)
		if ferr != nil {
			if errors.Is(ferr, hosted.ErrLoginRequired) {
				return 0, ferr
			}
			if errors.Is(ferr, hosted.ErrDocumentFileMissing) {
				continue // listed then deleted — skip
			}
			return 0, fmt.Errorf("fetch document %s (%s): %w", item.Path, label, ferr)
		}
		files = append(files, subsync.File{Path: item.Path, Content: content})
	}
	if len(files) > 0 {
		if _, err := subsync.Restore(dataDir, files); err != nil {
			return 0, fmt.Errorf("restore documents (%s): %w", label, err)
		}
	}
	if changes.Cursor != "" {
		if err := hosted.SaveDocumentCursor(server, wsID, absRoot, changes.Cursor); err != nil {
			return 0, fmt.Errorf("save document cursor (%s): %w", label, err)
		}
	}
	return len(files), nil
}
