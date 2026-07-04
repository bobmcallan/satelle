package cli

// `satelle project create` / `list` — the CLI counterpart to the hosted-server
// projects API (sty_2da92fd5). create POSTs to /api/v1/projects (the caller
// becomes owner); list GETs the caller's projects so they can confirm a create
// and pick one for later document operations. Both reuse the login flow's
// resolveServer (--server → [hosted] server in satelle.toml) and the per-user
// credential store. Like login/whoami these commands never touch the local DB,
// so they carry no store annotation and work in a fresh clone.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/hosted"
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
	create.Flags().StringVar(&createServer, "server", "", "Hosted server URL (overrides [hosted] server in satelle.toml).")
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
	list.Flags().StringVar(&listServer, "server", "", "Hosted server URL (overrides [hosted] server in satelle.toml).")
	project.AddCommand(list)

	register(project)
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
		return fmt.Errorf("no hosted server configured — pass --server <url> (or set [hosted] server in satelle.toml)")
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
		return fmt.Errorf("no hosted server configured — pass --server <url> (or set [hosted] server in satelle.toml)")
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
