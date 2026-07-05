package cli

// `satelle project create` / `list` — the CLI counterpart to the hosted-server
// projects API (sty_2da92fd5). create POSTs to /api/v1/projects (the caller
// becomes owner); list GETs the caller's projects so they can confirm a create
// and pick one for later document operations. Both reuse the login flow's
// resolveServer (--server → global [hosted] server → repo satelle.toml) and the per-user
// credential store. Like login/whoami these commands never touch the local DB,
// so they carry no store annotation and work in a fresh clone.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/subsync"
	"github.com/spf13/cobra"
)

func init() {
	project := &cobra.Command{
		Use:   "project",
		Short: "Manage projects on the hosted satelle-server",
	}

	var (
		createServer string
		slug         string
		name         string
	)
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a project on the hosted server (you become its owner)",
		Long: `create POSTs to the hosted server's /api/v1/projects endpoint using the
stored credentials, making you the new project's owner, and prints its id, slug,
and name. Requires a prior "satelle login".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectCreate(cmd, createServer, slug, name)
		},
	}
	create.Flags().StringVar(&createServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	create.Flags().StringVar(&slug, "slug", "", "Project slug (required).")
	create.Flags().StringVar(&name, "name", "", "Human-readable project name (required).")
	project.AddCommand(create)

	var listServer string
	list := &cobra.Command{
		Use:   "list",
		Short: "List your projects on the hosted server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectList(cmd, listServer)
		},
	}
	list.Flags().StringVar(&listServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	project.AddCommand(list)

	// bind — record which hosted project this repo backs up to (AC1).
	bind := &cobra.Command{
		Use:   "bind <slug>",
		Short: "Bind this repo to a hosted project slug (for push/pull backup)",
		Long: `bind records which hosted project this repo backs up to via
"satelle project push"/"pull". The slug is written to the committed
.satelle/satelle.toml [hosted] project key — secret-free, since tokens live only
in the per-user credential store. "satelle project show" prints the binding.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectBind(cmd, args[0])
		},
	}
	project.AddCommand(bind)

	var showServer string
	show := &cobra.Command{
		Use:   "show",
		Short: "Show this repo's hosted server, bound project, and sign-in state",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectShow(cmd, showServer)
		},
	}
	show.Flags().StringVar(&showServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	project.AddCommand(show)

	// push — back up this repo's authored .satelle/ substrate (AC2, AC5).
	var (
		pushServer  string
		pushProject string
	)
	push := &cobra.Command{
		Use:   "push",
		Short: "Back up this repo's authored .satelle/ substrate to its bound hosted project",
		Long: `push bundles this repo's git-TRACKED authored substrate under .satelle/
and uploads it to the bound hosted project, replacing the project's stored
snapshot. Only authored, git-tracked files are sent — local/generated state
(satelle.db + WAL/SHM, logs/, backups/, generated story/index views,
satelle.local.toml, the pinned binary) is never uploaded. An identical re-push is
idempotent (the server reports all-unchanged).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectPush(cmd, pushServer, pushProject)
		},
	}
	push.Flags().StringVar(&pushServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	push.Flags().StringVar(&pushProject, "project", "", "Project slug (overrides the repo's bound project).")
	project.AddCommand(push)

	// pull — restore this repo's authored .satelle/ substrate (AC3).
	var (
		pullServer  string
		pullProject string
		pullDir     string
	)
	pull := &cobra.Command{
		Use:   "pull",
		Short: "Restore this repo's authored .satelle/ substrate from its bound hosted project",
		Long: `pull downloads the bound hosted project's stored substrate snapshot and
reconstructs .satelle/ from it, byte-for-byte. It writes only authored files and
never touches local-only paths (satelle.db, the local overlay). Use --dir to
restore into a different repo root (e.g. a clean checkout).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectPull(cmd, pullServer, pullProject, pullDir)
		},
	}
	pull.Flags().StringVar(&pullServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	pull.Flags().StringVar(&pullProject, "project", "", "Project slug (overrides the repo's bound project).")
	pull.Flags().StringVar(&pullDir, "dir", "", "Repo root to restore into (default: the current repo).")
	project.AddCommand(pull)

	register(project)
}

// resolveProjectTarget resolves the hosted server, the bound project slug, and
// the repo root for a substrate push/pull. server precedence = --server → the
// configured global/repo server; slug precedence = --project → the repo's bound
// hosted.project. It errors clearly when either is missing.
func resolveProjectTarget(serverArg, projectArg string) (server, slug, repoRoot string, err error) {
	cfg, cfgPath, lerr := config.Load("")
	if lerr != nil && !errors.Is(lerr, config.ErrNotFound) {
		return "", "", "", fmt.Errorf("load config: %w", lerr)
	}
	server = strings.TrimRight(strings.TrimSpace(serverArg), "/")
	if server == "" {
		server = config.ResolveHostedServer(cfg)
	}
	if server == "" {
		return "", "", "", fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	slug = strings.TrimSpace(projectArg)
	if slug == "" {
		slug = strings.TrimSpace(cfg.Hosted.Project)
	}
	if slug == "" {
		return "", "", "", fmt.Errorf("no bound project — run \"satelle project bind <slug>\" or pass --project <slug>")
	}
	return server, slug, config.RepoRootFromConfigPath(cfgPath), nil
}

func runProjectBind(cmd *cobra.Command, slug string) error {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return fmt.Errorf("a project slug is required")
	}
	_, cfgPath, err := config.Load("")
	if err != nil && !errors.Is(err, config.ErrNotFound) {
		return fmt.Errorf("load config: %w", err)
	}
	if cfgPath == "" {
		return fmt.Errorf("not in a satelle repo — run \"satelle init\" first")
	}
	edit := config.KeyEdit{Section: "hosted", Key: "project", Value: strconv.Quote(slug)}
	if err := config.SaveConfigValues(cfgPath, []config.KeyEdit{edit}); err != nil {
		return fmt.Errorf("record hosted project in satelle.toml: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Bound this repo to project %q (recorded in %s).\n", slug, cfgPath)
	return nil
}

func runProjectShow(cmd *cobra.Command, serverArg string) error {
	cfg, _, err := config.Load("")
	if err != nil && !errors.Is(err, config.ErrNotFound) {
		return fmt.Errorf("load config: %w", err)
	}
	server := strings.TrimRight(strings.TrimSpace(serverArg), "/")
	if server == "" {
		server = config.ResolveHostedServer(cfg)
	}
	out := cmd.OutOrStdout()
	if server == "" {
		fmt.Fprintln(out, "hosted server: (none configured)")
	} else {
		fmt.Fprintf(out, "hosted server: %s\n", server)
	}
	if slug := strings.TrimSpace(cfg.Hosted.Project); slug == "" {
		fmt.Fprintln(out, "bound project: (none — run \"satelle project bind <slug>\")")
	} else {
		fmt.Fprintf(out, "bound project: %s\n", slug)
	}
	signed := "signed out"
	if server != "" {
		if _, lerr := (hosted.FileStore{}).Load(server); lerr == nil {
			signed = "signed in"
		}
	}
	fmt.Fprintf(out, "sign-in state: %s\n", signed)
	return nil
}

func runProjectPush(cmd *cobra.Command, serverArg, projectArg string) error {
	server, slug, repoRoot, err := resolveProjectTarget(serverArg, projectArg)
	if err != nil {
		return err
	}
	files, err := subsync.Bundle(repoRoot)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no git-tracked authored substrate under %s/.satelle — nothing to push", repoRoot)
	}
	sum, err := hosted.NewClient(server, hosted.FileStore{}, nil).PushSubstrate(cmd.Context(), slug, files)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return err
		}
		return fmt.Errorf("push substrate: %w", err)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Pushed %d authored file(s) to project %q on %s.\n", len(files), slug, server)
	fmt.Fprintf(out, "  added %d · updated %d · unchanged %d · removed %d\n", sum.Added, sum.Updated, sum.Unchanged, sum.Removed)
	if sum.Added == 0 && sum.Updated == 0 && sum.Removed == 0 {
		fmt.Fprintln(out, "  (already up to date — idempotent re-push)")
	}
	return nil
}

func runProjectPull(cmd *cobra.Command, serverArg, projectArg, dir string) error {
	server, slug, repoRoot, err := resolveProjectTarget(serverArg, projectArg)
	if err != nil {
		return err
	}
	if d := strings.TrimSpace(dir); d != "" {
		repoRoot = d
	}
	files, err := hosted.NewClient(server, hosted.FileStore{}, nil).PullSubstrate(cmd.Context(), slug)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return err
		}
		return fmt.Errorf("pull substrate: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("project %q has no substrate backup yet — run \"satelle project push\" first", slug)
	}
	n, err := subsync.Restore(repoRoot, files)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Restored %d authored file(s) from project %q on %s into %s/.satelle.\n", n, slug, server, repoRoot)
	return nil
}

func runProjectCreate(cmd *cobra.Command, serverArg, slug, name string) error {
	// Validate client-side BEFORE any network construction (AC2).
	slug, name = strings.TrimSpace(slug), strings.TrimSpace(name)
	if slug == "" {
		return fmt.Errorf("--slug is required")
	}
	if name == "" {
		return fmt.Errorf("--name is required")
	}
	server, _ := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}

	p, err := hosted.NewClient(server, hosted.FileStore{}, nil).CreateProject(cmd.Context(), slug, name)
	if err != nil {
		switch {
		case errors.Is(err, hosted.ErrLoginRequired):
			return err
		case errors.Is(err, hosted.ErrSlugConflict):
			return fmt.Errorf("slug %q already exists on %s", slug, server)
		default:
			return fmt.Errorf("create project: %w", err)
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Created project on %s:\n  id:   %s\n  slug: %s\n  name: %s\n", server, p.ID, p.Slug, p.Name)
	return nil
}

func runProjectList(cmd *cobra.Command, serverArg string) error {
	server, _ := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}

	projects, err := hosted.NewClient(server, hosted.FileStore{}, nil).ListProjects(cmd.Context())
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return err
		}
		return fmt.Errorf("list projects: %w", err)
	}
	out := cmd.OutOrStdout()
	if len(projects) == 0 {
		fmt.Fprintf(out, "No projects on %s.\n", server)
		return nil
	}
	fmt.Fprintf(out, "%-24s  %-20s  %-24s  %s\n", "ID", "SLUG", "NAME", "ROLE")
	for _, p := range projects {
		role := p.Role
		if role == "" {
			role = "-"
		}
		fmt.Fprintf(out, "%-24s  %-20s  %-24s  %s\n", p.ID, p.Slug, p.Name, role)
	}
	return nil
}
