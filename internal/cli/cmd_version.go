package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	register(&cobra.Command{
		Use:   "version",
		Short: "Print build info",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), versionLine())
			return nil
		},
	})
}
