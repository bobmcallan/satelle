// `satelle trunk` — report how this repo's local trunk stands against its
// remote (sty_9f3e51d1). It runs the same internal/trunk unit the engage path
// runs, so an operator, the release flow and the worktree-cutting flow read the
// same answer the engage-time report gives.
package cli

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
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
		strict      bool
		refuse      []string
	)
	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Report how local trunk stands against the remote; --fast-forward brings in remote commits when safe",
		Long: `Fetches the remote's trunk branch and reports one state: level, behind, ahead,
diverged, dirty, offline or skipped. It is the report story engagement prints
before work starts. Any reported state exits 0 unless --strict is given;
--json prints the report object.

--strict exits non-zero, after the report, when the state is in the stop set
([trunk] base_refuse, or --refuse); a trunk it cannot name is unresolved: pass
--trunk-branch. The epic merge step runs it before merging.

--fast-forward advances a behind trunk with 'merge --ff-only', and only when this
working tree has trunk checked out and clean. See 'satelle help trunk'.

  satelle trunk sync --fast-forward --json`,
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			cfg := a.PlaneConfig().Trunk
			stopSet := cfg.BaseRefuseSet()
			if cmd.Flags().Changed("refuse") {
				if err := config.ValidateTrunkStates(refuse); err != nil {
					return fmt.Errorf("--refuse %w", err)
				}
				stopSet = refuse
			}
			if branch == "" {
				branch = cfg.Branch
			}
			rep := trunk.Check(cmd.Context(), a.RepoRoot, trunk.Options{
				Remote: remote, Branch: branch, FastForward: fastForward, ResolveHead: true,
			})
			if asJSON {
				b, err := json.MarshalIndent(rep, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), rep.Line())
			}
			if state := trunk.StopState(rep); strict && state != "" && slices.Contains(stopSet, state) {
				return fmt.Errorf("trunk %s — %s", rep.Detail(), rep.Hint())
			}
			return nil
		},
	}
	syncCmd.Flags().BoolVar(&fastForward, "fast-forward", false, "advance a behind trunk with merge --ff-only when this working tree has it checked out and clean")
	syncCmd.Flags().BoolVar(&asJSON, "json", false, "print the report as JSON")
	syncCmd.Flags().StringVar(&remote, "remote", "", "remote to compare against (default origin)")
	syncCmd.Flags().StringVar(&branch, "trunk-branch", "", "trunk branch when the remote's HEAD ref does not name one (default: [trunk] branch)")
	syncCmd.Flags().StringVar(&branch, "branch", "", "deprecated alias of --trunk-branch")
	_ = syncCmd.Flags().MarkDeprecated("branch", "use --trunk-branch")
	syncCmd.Flags().BoolVar(&strict, "strict", false, "exit non-zero, after printing the report, when the trunk is in a state of the stop set")
	syncCmd.Flags().StringSliceVar(&refuse, "refuse", nil, "stop set for --strict, replacing [trunk] base_refuse (any of dirty, diverged, ahead, behind, offline, unresolved)")
	group.AddCommand(syncCmd)
	register(group)
}
