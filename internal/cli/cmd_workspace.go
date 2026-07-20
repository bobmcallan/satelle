// `satelle workspace` manages the connected-repo registry the /workspace view
// aggregates (build order step 6). The registry lives in the global config
// (~/.satelle/config.toml [workspace] repos); per-repo databases stay the source
// of truth. `workspace add` also seeds the push-fed serve mirror, bootstrapping
// [server] endpoint into satelle.local.toml when a local serve is reachable
// (sty_805bee9c / sty_0122610a / epic:serve-adoption).
package cli

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
)

func init() {
	ws := &cobra.Command{
		Use:   "workspace",
		Short: "Manage the connected-repo registry the workspace view aggregates",
	}

	add := &cobra.Command{
		Use:   "add [path]",
		Short: "Join the workspace: register, seed the serve mirror (bootstraps [server] endpoint when a local serve is running)",
		Long: `workspace add registers a repo in the machine-local workspace registry
(~/.satelle/config.toml [workspace] repos) and seeds the push-fed serve mirror
so the project appears on the landing.

[server] endpoint is required to seed. It usually lives in the gitignored
per-machine .satelle/satelle.local.toml (not committed satelle.toml). When no
endpoint is set and a local serve answers at the global service port (default
http://127.0.0.1:8787), workspace add writes that endpoint into local.toml and
seeds in the same command. A value already present in satelle.toml or local.toml
is respected and not overwritten.

If no endpoint is set and no serve answers, the verb still registers the path
but exits non-zero with the exact file, keys, default URL, and re-run command —
the landing will not show a card until seed succeeds.

This is the single join verb for the local UI (epic:serve-adoption). Later
mutations are pushed automatically by the CLI change publisher — re-run
workspace add only to re-seed the mirror manually.

Path defaults to the current directory. When the path is a different repo than
the one opened by the CLI, only registration runs — seed from inside that repo.`,
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
// (or can be bootstrapped from a live local serve) and the path is the active
// repo (sty_805bee9c / sty_0122610a).
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
	ep, bootstrapped, err := resolveSeedEndpoint(a, gc)
	if err != nil {
		return err
	}
	if bootstrapped {
		fmt.Fprintf(cmd.OutOrStdout(), "workspace add: bootstrapped [server] endpoint = %q into %s\n",
			ep, localTomlPath(a))
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

// resolveSeedEndpoint returns the serve base URL to seed against. A configured
// [server] endpoint (committed or local overlay, already merged into a.Config)
// wins unchanged. Otherwise, when a serve answers at the global service port,
// the candidate is written to satelle.local.toml and returned. When neither is
// available, registration has already happened; the error names the remedy
// (file, keys, default URL, re-run) so the join fails non-zero (sty_0122610a).
func resolveSeedEndpoint(a *app.App, gc config.GlobalConfig) (endpoint string, bootstrapped bool, err error) {
	if ep := strings.TrimSpace(a.Config.Server.Endpoint); ep != "" {
		return ep, false, nil
	}
	candidate := defaultServerEndpoint(gc)
	if !healthzOK(strings.TrimRight(candidate, "/") + "/healthz") {
		localPath := localTomlPath(a)
		return "", false, fmt.Errorf(
			"workspace add: registered, but the mirror was not seeded — the project will NOT appear on the landing.\n"+
				"No [server] endpoint is set and no serve answered %s/healthz.\n"+
				"Start it (`satelle serve`, or `satelle service install`), then re-run\n"+
				"  satelle workspace add\n"+
				"To point at a different serve, add to %s:\n"+
				"  [server]\n"+
				"  endpoint = %q",
			candidate, localPath, candidate)
	}
	localPath := localTomlPath(a)
	edit := config.KeyEdit{Section: "server", Key: "endpoint", Value: strconv.Quote(candidate)}
	if err := config.SaveConfigValues(localPath, []config.KeyEdit{edit}); err != nil {
		return "", false, fmt.Errorf("workspace add: bootstrap [server] endpoint into %s: %w", localPath, err)
	}
	// Keep the in-memory config in step so any same-process reader sees it.
	a.Config.Server.Endpoint = candidate
	return candidate, true, nil
}

// defaultServerEndpoint is the bootstrap candidate from the global service port
// (DefaultWebPort when unset). Always 127.0.0.1 — the CLI publishes to a local
// serve, not a remote bind address.
func defaultServerEndpoint(gc config.GlobalConfig) string {
	return fmt.Sprintf("http://127.0.0.1:%d", gc.Service.ResolvePort())
}

// localTomlPath is the gitignored per-machine overlay beside the committed
// satelle.toml for the active repo.
func localTomlPath(a *app.App) string {
	return filepath.Join(a.DataDir, config.LocalConfigName)
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
