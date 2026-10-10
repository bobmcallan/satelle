// `satelle trunk` — report how this repo's local trunk stands against its
// remote (sty_9f3e51d1). It runs the same internal/trunk unit the engage path
// runs, so an operator, the release flow and the worktree-cutting flow read the
// same answer the engage-time report gives.
package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/trunk"
)

// publishCommand is `satelle trunk publish` (sty_6af229f1): the mechanism a
// release invokes to put its commits on a trunk other machines have moved. The
// proof and the version stamp are the repo's [trunk] declaration (or flags); the
// command compiles in neither.
func publishCommand() *cobra.Command {
	var (
		asJSON bool
		remote string
		branch string
		prove  string
		stamp  string
		rounds int
		story  string
	)
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Bring the remote's trunk in, prove the combined head, and push it without force",
		Long: `Publishes this tree's trunk: merges in what other machines pushed, runs the
[trunk] stamp and prove commands on the combined head, then pushes it with a
plain push. A push refused because the trunk moved is answered by another round,
up to [trunk] publish_rounds; nothing is ever forced. A conflict, a failed
proof or a spent bound exits non-zero with trunk put back and the remote
untouched. --story ledgers the pushed and combined heads. See 'satelle help trunk'.

  satelle trunk publish --story sty_123 --json`,
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			cfg := a.PlaneConfig().Trunk
			opts := trunk.PublishOptions{
				Remote: remote, Branch: branch,
				Prove:  firstNonEmpty(prove, cfg.Prove),
				Stamp:  firstNonEmpty(stamp, cfg.Stamp),
				Rounds: rounds,
			}
			if opts.Rounds == 0 {
				opts.Rounds = cfg.PublishRoundBound()
			}
			rep := trunk.Publish(cmd.Context(), a.RepoRoot, opts)
			if story != "" {
				payload, _ := json.Marshal(rep)
				_, _ = a.Store.Ledger.Append(cmd.Context(), ledger.AppendInput{
					StoryID: story, Kind: ledger.KindTrunkPublish, Actor: "executor",
					Body: rep.Line(), Payload: payload,
				}, time.Now())
			}
			if asJSON {
				b, err := json.MarshalIndent(rep, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), rep.Line())
			}
			if !rep.OK() {
				cmd.SilenceUsage = true
				return fmt.Errorf("trunk publish: %s", rep.Error)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the report as JSON")
	cmd.Flags().StringVar(&remote, "remote", "", "remote to publish to (default origin)")
	cmd.Flags().StringVar(&branch, "branch", "", "trunk branch when the remote's HEAD ref does not name one")
	cmd.Flags().StringVar(&prove, "prove", "", "shell command that proves the combined head (default [trunk] prove)")
	cmd.Flags().StringVar(&stamp, "stamp", "", "shell command that commits the version bump on the integrated tree (default [trunk] stamp)")
	cmd.Flags().IntVar(&rounds, "rounds", 0, "most rounds a refused push is answered with (default [trunk] publish_rounds, else 5)")
	cmd.Flags().StringVar(&story, "story", "", "story id to record a trunk_publish ledger row on")
	return cmd
}

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
	group.AddCommand(publishCommand())
	register(group)
}
