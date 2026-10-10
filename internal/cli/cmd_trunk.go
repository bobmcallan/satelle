// `satelle trunk` — report how this repo's local trunk stands against its
// remote (sty_9f3e51d1). It runs the same internal/trunk unit the engage path
// runs, so an operator, the release flow and the worktree-cutting flow read the
// same answer the engage-time report gives.
package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/trunk"
)

func init() {
	group := &cobra.Command{
		Use:   "trunk",
		Short: "Check the local trunk branch against its remote",
		Long: `Compares the local trunk branch with its remote's. 'satelle trunk sync' runs the
check story engagement runs before work starts; see 'satelle help trunk'.`,
	}

	var (
		fastForward bool
		asJSON      bool
		remote      string
		branch      string
	)
	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Report how local trunk stands against the remote; --fast-forward brings in remote commits when safe",
		Long: `Fetches the remote's trunk branch and reports one state: level, behind, ahead,
diverged, dirty, offline or skipped. It is the report story engagement prints
before work starts. Any reported state exits 0; --json prints the report object.

--fast-forward advances a behind trunk with 'merge --ff-only', and only when this
working tree has trunk checked out and clean. Trunk is never merged, rebased,
reset, stashed or committed otherwise. See 'satelle help trunk'.

  satelle trunk sync --fast-forward --json`,
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			rep := trunk.Check(cmd.Context(), a.RepoRoot, trunk.Options{
				Remote: remote, Branch: branch, FastForward: fastForward,
			})
			if asJSON {
				b, err := json.MarshalIndent(rep, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), rep.Line())
			return nil
		},
	}
	syncCmd.Flags().BoolVar(&fastForward, "fast-forward", false, "advance a behind trunk with merge --ff-only when this working tree has it checked out and clean")
	syncCmd.Flags().BoolVar(&asJSON, "json", false, "print the report as JSON")
	syncCmd.Flags().StringVar(&remote, "remote", "", "remote to compare against (default origin)")
	syncCmd.Flags().StringVar(&branch, "branch", "", "trunk branch when the remote's HEAD ref does not name one")
	group.AddCommand(syncCmd)
	register(group)
}
