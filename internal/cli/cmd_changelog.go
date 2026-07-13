package cli

import (
	"github.com/spf13/cobra"
)

func init() {
	var from, to string
	cmd := &cobra.Command{
		Use:   "changelog",
		Short: "Retrieve changelog entries between two versions (no git history)",
		Long: `changelog returns entries in the range (from, to] from the CHANGELOG
embedded in this binary (the only consumer channel — works in any repo with no
satelle source tree, no local CHANGELOG.md, no git, no network). to defaults to
the installed binary version. Breaking versions carry a non-empty ### Breaking
subsection — the marker require-init and post-upgrade heal key on.

Repo-root CHANGELOG.md is the build input that ships into the embed; consumers
never depend on it (sty_b5fa838a).

  satelle changelog
  satelle changelog --from 0.0.200 --to 0.0.217`,
		Args: cobra.NoArgs,
		// No store: pure parse of the embedded changelog (same class as version).
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
