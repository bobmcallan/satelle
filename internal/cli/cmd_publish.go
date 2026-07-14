package cli

// `satelle publish` — team catalog (epic:sync-publish). Distinct from sync:
// sync is personal-only; publish exposes selected local artifacts into a
// connected team workspace under the developer's publisher identity.
// adopt copies a published artifact into the local personal repo.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/subsync"
)

func init() {
	pub := &cobra.Command{
		Use:   "publish",
		Short: "Publish local satelle artifacts to a team catalog (not sync)",
	}
	var pubServer, pubWorkspace, pubKind, pubTitle string
	var dryRun bool
	push := &cobra.Command{
		Use:   "push [path...]",
		Short: "Publish local file(s) into the team catalog",
		Long: `push publishes one or more paths under .satelle/ (relative to the data dir,
e.g. documents/note.md or skills/foo.md) into the connected team workspace.
Requires a team workspace (--workspace or [hosted] workspace = team name).
The publisher retains control of later versions; other members adopt copies.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPublishPush(cmd, pubServer, pubWorkspace, pubKind, pubTitle, dryRun, args)
		},
	}
	push.Flags().StringVar(&pubServer, "server", "", "Hosted server URL")
	push.Flags().StringVar(&pubWorkspace, "workspace", "", "Team workspace name (required if not bound)")
	push.Flags().StringVar(&pubKind, "kind", "", "Artifact kind (document|workflow|skill|principle|…). Default: inferred from path")
	push.Flags().StringVar(&pubTitle, "title", "", "Optional display title")
	push.Flags().BoolVar(&dryRun, "dry-run", false, "List what would be published without contacting the server")
	pub.AddCommand(push)

	var listServer, listWorkspace string
	list := &cobra.Command{
		Use:   "list",
		Short: "List published artifacts on a team workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPublishList(cmd, listServer, listWorkspace)
		},
	}
	list.Flags().StringVar(&listServer, "server", "", "Hosted server URL")
	list.Flags().StringVar(&listWorkspace, "workspace", "", "Team workspace name")
	pub.AddCommand(list)

	var adoptServer, adoptWorkspace string
	var adoptVersion int
	adopt := &cobra.Command{
		Use:   "adopt <path>",
		Short: "Copy a published artifact into this repo (personal copy)",
		Long: `adopt writes a personal copy of a team-published path into .satelle/.
Edits never overwrite the publisher's canonical entry. Source linkage is
recorded in .satelle/adoptions.json for update checks.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPublishAdopt(cmd, adoptServer, adoptWorkspace, adoptVersion, args[0])
		},
	}
	adopt.Flags().StringVar(&adoptServer, "server", "", "Hosted server URL")
	adopt.Flags().StringVar(&adoptWorkspace, "workspace", "", "Team workspace name")
	adopt.Flags().IntVar(&adoptVersion, "version", 0, "Pin a published version (default: latest)")
	pub.AddCommand(adopt)

	var checkServer, checkWorkspace string
	var doUpdate bool
	check := &cobra.Command{
		Use:   "check",
		Short: "Show adopted items with a newer published version; optionally update",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPublishCheck(cmd, checkServer, checkWorkspace, doUpdate)
		},
	}
	check.Flags().StringVar(&checkServer, "server", "", "Hosted server URL")
	check.Flags().StringVar(&checkWorkspace, "workspace", "", "Team workspace name")
	check.Flags().BoolVar(&doUpdate, "update", false, "Opt in: write newer published bytes into local copies")
	pub.AddCommand(check)

	register(pub)
}

// resolveTeamWorkspaceName returns the team workspace name for publish (and
// similar team-bound verbs): --workspace wins, else the configured active
// workspace when it is a team, else "". Sync never uses this as a destination.
func resolveTeamWorkspaceName(cfg config.Config, workspaceArg string) string {
	if ws := strings.TrimSpace(workspaceArg); ws != "" {
		return ws
	}
	active := cfg.ResolveActiveWorkspace()
	if active.IsPersonal() {
		return ""
	}
	return active.Name
}

func resolveTeamForPublish(cfg config.Config, workspaceArg string) (string, error) {
	name := resolveTeamWorkspaceName(cfg, workspaceArg)
	if name == "" {
		return "", fmt.Errorf("no team workspace — run \"satelle login --workspace <team>\" or pass --workspace <team> (publish requires a team connection)")
	}
	return name, nil
}

func inferPublishKind(path string) string {
	p := strings.ToLower(path)
	switch {
	case strings.HasPrefix(p, "documents/"):
		return "document"
	case strings.HasPrefix(p, "workflows/"):
		return "workflow"
	case strings.HasPrefix(p, "skills/"):
		return "skill"
	case strings.HasPrefix(p, "principles/"):
		return "principle"
	case p == "constitution.md" || strings.HasPrefix(p, "constitution"):
		return "constitution"
	case p == "agents.toml" || strings.HasPrefix(p, "agents"):
		return "agents"
	case strings.HasPrefix(p, "tasks/"):
		return "task"
	case strings.HasPrefix(p, "stories/"):
		return "story"
	default:
		return "other"
	}
}

func runPublishPush(cmd *cobra.Command, serverArg, workspaceArg, kind, title string, dryRun bool, paths []string) error {
	cfg, repoRoot, dataDir, err := loadRepoConfig()
	if err != nil {
		return err
	}
	server := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	teamName, err := resolveTeamForPublish(cfg, workspaceArg)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if dryRun {
		fmt.Fprintf(out, "Would publish %d path(s) to team workspace %q on %s:\n", len(paths), teamName, server)
		for _, p := range paths {
			k := kind
			if k == "" {
				k = inferPublishKind(p)
			}
			fmt.Fprintf(out, "  [%s] %s\n", k, p)
		}
		return nil
	}
	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	wsID, err := client.ActiveWorkspaceID(cmd.Context(), teamName)
	if err != nil {
		return fmt.Errorf("resolve team workspace: %w", err)
	}
	var created, skipped int
	for _, rel := range paths {
		rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
		rel = strings.TrimPrefix(rel, ".satelle/")
		abs := filepath.Join(dataDir, filepath.FromSlash(rel))
		// Also allow paths relative to repo root under .satelle/
		if _, err := os.Stat(abs); err != nil {
			abs2 := filepath.Join(repoRoot, ".satelle", filepath.FromSlash(rel))
			if _, err2 := os.Stat(abs2); err2 == nil {
				abs = abs2
			}
		}
		content, err := os.ReadFile(abs)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		k := kind
		if k == "" {
			k = inferPublishKind(rel)
		}
		item, perr := client.PublishFile(cmd.Context(), wsID, rel, k, title, content)
		if perr != nil {
			if errors.Is(perr, hosted.ErrLoginRequired) {
				return perr
			}
			if errors.Is(perr, hosted.ErrNotPublisher) {
				return fmt.Errorf("publish %s: %w", rel, perr)
			}
			return fmt.Errorf("publish %s: %w", rel, perr)
		}
		if item.Created {
			created++
			fmt.Fprintf(out, "  published %s v%d (new)\n", rel, item.Version)
		} else {
			skipped++
			fmt.Fprintf(out, "  %s unchanged at v%d\n", rel, item.Version)
		}
	}
	fmt.Fprintf(out, "Published to team %q on %s: %d new, %d unchanged.\n", teamName, server, created, skipped)
	return nil
}

func runPublishList(cmd *cobra.Command, serverArg, workspaceArg string) error {
	cfg, _, _, err := loadRepoConfig()
	if err != nil {
		return err
	}
	server := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	teamName, err := resolveTeamForPublish(cfg, workspaceArg)
	if err != nil {
		return err
	}
	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	wsID, err := client.ActiveWorkspaceID(cmd.Context(), teamName)
	if err != nil {
		return fmt.Errorf("resolve team workspace: %w", err)
	}
	items, err := client.ListPublished(cmd.Context(), wsID)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return err
		}
		return err
	}
	out := cmd.OutOrStdout()
	if len(items) == 0 {
		fmt.Fprintf(out, "No published artifacts in team workspace %q.\n", teamName)
		return nil
	}
	for _, it := range items {
		fmt.Fprintf(out, "%s\tv%d\t%s\t%s\n", it.Path, it.Version, it.Kind, it.PublisherID)
	}
	return nil
}

func runPublishAdopt(cmd *cobra.Command, serverArg, workspaceArg string, version int, path string) error {
	cfg, _, dataDir, err := loadRepoConfig()
	if err != nil {
		return err
	}
	server := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	teamName, err := resolveTeamForPublish(cfg, workspaceArg)
	if err != nil {
		return err
	}
	path = strings.TrimPrefix(filepath.ToSlash(path), "./")
	path = strings.TrimPrefix(path, ".satelle/")
	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	wsID, err := client.ActiveWorkspaceID(cmd.Context(), teamName)
	if err != nil {
		return fmt.Errorf("resolve team workspace: %w", err)
	}
	content, meta, err := client.PublishedContent(cmd.Context(), wsID, path, version)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) || errors.Is(err, hosted.ErrPublishNotFound) {
			return err
		}
		return fmt.Errorf("fetch published %s: %w", path, err)
	}
	if res, err := subsync.Restore(dataDir, []subsync.File{{Path: path, Content: content}}); err != nil {
		return fmt.Errorf("write local copy: %w", err)
	} else if res.Written == 0 && len(res.Skipped) > 0 {
		// Single-file adopt: an excluded path is a caller error, not a batch to skip past (sty_84f14ace).
		return fmt.Errorf("write local copy: %q is a local-only path — refusing to adopt", path)
	}
	if err := saveAdoption(dataDir, adoptionRecord{
		Workspace: teamName, Path: path, Version: meta.Version, Kind: meta.Kind, PublisherID: meta.PublisherID,
	}); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Adopted %s v%d from team %q into %s (personal copy).\n", path, meta.Version, teamName, dataDir)
	return nil
}

func runPublishCheck(cmd *cobra.Command, serverArg, workspaceArg string, doUpdate bool) error {
	_, _, dataDir, err := loadRepoConfig()
	if err != nil {
		return err
	}
	server := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	recs, err := loadAdoptions(dataDir)
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No adopted artifacts recorded.")
		return nil
	}
	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	out := cmd.OutOrStdout()
	var updates int
	for _, rec := range recs {
		team := rec.Workspace
		if workspaceArg != "" {
			team = workspaceArg
		}
		wsID, err := client.ActiveWorkspaceID(cmd.Context(), team)
		if err != nil {
			return fmt.Errorf("resolve workspace %q: %w", team, err)
		}
		content, meta, err := client.PublishedContent(cmd.Context(), wsID, rec.Path, 0)
		if err != nil {
			if errors.Is(err, hosted.ErrPublishNotFound) {
				fmt.Fprintf(out, "%s: no longer published on %q\n", rec.Path, team)
				continue
			}
			return err
		}
		if meta.Version <= rec.Version {
			fmt.Fprintf(out, "%s: up to date (v%d)\n", rec.Path, rec.Version)
			continue
		}
		fmt.Fprintf(out, "%s: new version published (local v%d → remote v%d)\n", rec.Path, rec.Version, meta.Version)
		if !doUpdate {
			continue
		}
		if res, err := subsync.Restore(dataDir, []subsync.File{{Path: rec.Path, Content: content}}); err != nil {
			return fmt.Errorf("update %s: %w", rec.Path, err)
		} else if res.Written == 0 && len(res.Skipped) > 0 {
			// Single-file update: excluded path is a caller error (sty_84f14ace).
			return fmt.Errorf("update %s: local-only path — refusing to write", rec.Path)
		}
		rec.Version = meta.Version
		rec.PublisherID = meta.PublisherID
		if err := saveAdoption(dataDir, rec); err != nil {
			return err
		}
		updates++
		fmt.Fprintf(out, "  updated local copy to v%d\n", meta.Version)
	}
	if !doUpdate {
		fmt.Fprintln(out, "Re-run with --update to opt in to writing newer versions into local copies.")
	} else if updates == 0 {
		fmt.Fprintln(out, "No updates applied.")
	}
	return nil
}
