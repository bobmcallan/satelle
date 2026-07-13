package cli

import (
	"github.com/spf13/cobra"
)

func init() {
	var from, to string
	cmd := &cobra.Command{
		Use:   "changelog",
		Short: "Retrieve changelog entries between two versions (no git history)",
		Long: `changelog reads the well-known CHANGELOG.md at the repo root and returns
entries in the range (from, to]. to defaults to the installed binary version.
Breaking versions carry a non-empty ### Breaking subsection — the marker
require-init and post-upgrade heal key on.

  satelle changelog
  satelle changelog --from 0.0.200 --to 0.0.217`,
		Args: cobra.NoArgs,
		// No store: pure file read of CHANGELOG.md (same class as version).
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{}
			if from != "" {
				req["from"] = from
			}
			if to != "" {
				req["to"] = to
			}
			return dispatch(cmd, "changelog", req)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "exclusive lower bound version (omit for all up to --to)")
	cmd.Flags().StringVar(&to, "to", "", "inclusive upper bound (default: installed binary version)")
	register(cmd)
}
