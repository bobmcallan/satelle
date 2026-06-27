// `satelle workspace` manages the connected-repo registry the /workspace view
// aggregates (build order step 6). The registry lives in the global config
// (~/.satelle/config.toml [workspace] repos); per-repo databases stay the source
// of truth.
package cli

import (
	"fmt"
	"path/filepath"

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
		Short: "Register a repo (defaults to the current directory)",
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
			if gc.Workspace.AddRepo(path) {
				if err := config.SaveGlobal(gc); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "added %s (%d repos)\n", path, len(gc.Workspace.Repos))
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s already registered\n", path)
			}
			return nil
		},
	}

	remove := &cobra.Command{
		Use:     "remove [path]",
		Aliases: []string{"rm"},
		Short:   "Unregister a repo (defaults to the current directory)",
		Args:    cobra.MaximumNArgs(1),
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
				fmt.Fprintf(cmd.OutOrStdout(), "removed %s (%d repos)\n", path, len(gc.Workspace.Repos))
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

// resolveRepoArg returns the absolute repo path from an optional arg, defaulting
// to the current directory.
func resolveRepoArg(args []string) (string, error) {
	p := "."
	if len(args) == 1 {
		p = args[0]
	}
	return filepath.Abs(p)
}
