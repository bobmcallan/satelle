package cli

import (
	"github.com/spf13/cobra"
)

func init() {
	docCmd := &cobra.Command{Use: "doc", Short: "Read indexed authored docs (markdown source-of-truth)"}

	var lKind string
	listCmd := &cobra.Command{
		Use:         "list",
		Short:       "List indexed authored docs (optionally by kind)",
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{}
			putIf(req, "kind", lKind)
			return dispatch(cmd, "doc-list", req)
		},
	}
	listCmd.Flags().StringVar(&lKind, "kind", "", "filter by kind (documents|workflows|principles|skills)")

	getCmd := &cobra.Command{
		Use:         "get <kind> <name>",
		Short:       "Get one indexed authored doc by kind and name",
		Args:        cobra.ExactArgs(2),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "doc-get", map[string]any{"kind": args[0], "name": args[1]})
		},
	}

	docCmd.AddCommand(listCmd, getCmd)
	register(docCmd)
}
