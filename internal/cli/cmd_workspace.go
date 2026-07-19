// `satelle workspace` manages the connected-repo registry the /workspace view
// aggregates (build order step 6). The registry lives in the global config
// (~/.satelle/config.toml [workspace] repos); per-repo databases stay the source
// of truth. `workspace add` also seeds the push-fed serve mirror when [server]
// endpoint is configured (sty_805bee9c / epic:serve-adoption).
package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
)

func init() {
	ws := &cobra.Command{
		Use:   "workspace",
		Short: "Manage the connected-repo registry the workspace view aggregates",
	}

	add := &cobra.Command{
		Use:   "add [path]",
		Short: "Join the workspace: register the repo and seed the serve mirror",
		Long: `workspace add registers a repo in the machine-local workspace registry
(~/.satelle/config.toml [workspace] repos) and, when [server] endpoint is set in
satelle.toml, pushes a full snapshot so the push-fed serve mirror converges.

This is the single join verb for the local UI (epic:serve-adoption). Later
mutations are pushed automatically by the CLI change publisher — re-run
workspace add only to re-seed the mirror manually.

With no [server] endpoint the repo is still registered; a notice explains that
the mirror was not seeded. Path defaults to the current directory. When the
path is a different repo than the one opened by the CLI, only registration
runs — seed from inside that repo.`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: needsStore(),
		RunE:        runWorkspaceAdd,
	}

	remove := &cobra.Command{
		Use:   "remove [path]",
		Short: "Unregister a repo (defaults to the current directory)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveRepoArg(args)
			if err != nil {
				return err
			}
			gc, err := config.LoadGlobal()
			if err != nil {
				return err
			}
			if gc.Workspace.RemoveRepo(path) {
				if err := config.SaveGlobal(gc); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "removed %s (%d repos) — a running `satelle serve` stops serving it within a few seconds\n", path, len(gc.Workspace.Repos))
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s was not registered\n", path)
			}
			return nil
		},
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List registered repos",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			gc, err := config.LoadGlobal()
			if err != nil {
				return err
			}
			if len(gc.Workspace.Repos) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no repos registered — add one with `satelle workspace add`")
				return nil
			}
			for _, r := range gc.Workspace.Repos {
				fmt.Fprintln(cmd.OutOrStdout(), r)
			}
			return nil
		},
	}

	ws.AddCommand(add, remove, list)
	register(ws)
}

// runWorkspaceAdd registers the path then seeds the mirror when endpoint is set
// and the path is the active repo (sty_805bee9c).
func runWorkspaceAdd(cmd *cobra.Command, args []string) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	// Default path is the opened repo (config-resolved), not process cwd — so
	// SATELLE_CONFIG / sitting in a subdir still seeds the right store.
	var path string
	if len(args) == 1 {
		path, err = filepath.Abs(args[0])
		if err != nil {
			return err
		}
	} else {
		path, err = filepath.Abs(a.RepoRoot)
		if err != nil {
			return err
		}
	}
	gc, err := config.LoadGlobal()
	if err != nil {
		return err
	}
	added := gc.Workspace.AddRepo(path)
	if added {
		if err := config.SaveGlobal(gc); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "added %s (%d repos)\n", path, len(gc.Workspace.Repos))
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "%s already registered\n", path)
	}

	// Snapshot only the active repo's store — other paths are register-only.
	activeRoot, _ := filepath.Abs(a.RepoRoot)
	if filepath.Clean(path) != filepath.Clean(activeRoot) {
		fmt.Fprintf(cmd.OutOrStdout(), "workspace add: registered %s; run `satelle workspace add` from inside that repo to seed its mirror\n", path)
		return nil
	}
	ep := strings.TrimSpace(a.Config.Server.Endpoint)
	if ep == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "workspace add: registered; no [server] endpoint in satelle.toml — mirror not seeded (set it, then re-run)")
		return nil
	}
	snap, err := buildUISnapshot(cmd.Context(), a)
	if err != nil {
		return err
	}
	if err := postUISnapshot(ep, snap); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "workspace add: ok repo_key=%s stories=%d tasks=%d → %s/ingest/snapshot\n",
		snap.RepoKey, len(snap.Stories), len(snap.Tasks), strings.TrimRight(ep, "/"))
	return nil
}

// resolveRepoArg returns the absolute repo path from an optional arg, defaulting
// to the current directory.
func resolveRepoArg(args []string) (string, error) {
	p := "."
	if len(args) == 1 {
		p = args[0]
	}
	return filepath.Abs(p)
}
