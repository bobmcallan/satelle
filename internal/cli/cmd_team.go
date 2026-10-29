package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
)

func init() {
	team := &cobra.Command{
		Use:   "team",
		Short: "Resolve the team-owned substrate of a bound repo's project tier",
		Long: `In a repo bound to a project whose hosted store holds substrate, the paths
[team] paths declares (default agents.toml, workflows/**, skills/**) resolve from
the project tier — the deployed hosted version, which "satelle sync config deploy"
keeps under this repo's runtime dir — and the tree's copies of them do not
govern. Everything else resolves from the tree as before.`,
	}
	path := &cobra.Command{
		Use:   "path <rel>",
		Short: "Print where a .satelle-relative path resolves (the project tier, or the tree)",
		Long: `path prints the absolute path a .satelle-relative path resolves to: the project
tier's copy when the tier is present and the team owns the path, else the tree's
.satelle/<rel>. Substrate references a team script through it, so every command
runner — a dispatched step, a gate check, a trunk proof, the in-loop shell —
reaches the deployed bytes with no environment of its own:

  "$(satelle team path scripts/reviewer-token.sh)"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, repoRoot, _, err := loadRepoConfig()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), config.ResolveTeamPath(repoRoot, args[0]))
			return nil
		},
	}
	team.AddCommand(path)
	register(team)
}
