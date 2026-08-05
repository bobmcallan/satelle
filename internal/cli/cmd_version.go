package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	register(&cobra.Command{
		Use:   "version",
		Short: "Print build info",
		Long: `Print this binary's version, commit and build time, and whether it is the global
install or a repo-local pin.

Quote it when reporting anything: behaviour differs between builds, and the
commit is what makes a report reproducible. It opens no database, so it answers
even where satelle cannot govern.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), versionLine())
			return nil
		},
	})
}
