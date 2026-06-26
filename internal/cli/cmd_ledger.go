package cli

import (
	"github.com/spf13/cobra"
)

func init() {
	ledgerCmd := &cobra.Command{Use: "ledger", Short: "Append to and read the evidence ledger"}

	var aStory, aProject, aKind, aActor, aBody string
	appendCmd := &cobra.Command{
		Use:         "append",
		Short:       "Append an entry to the ledger",
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"kind": aKind}
			putIf(req, "story_id", aStory)
			putIf(req, "project_id", aProject)
			putIf(req, "actor", aActor)
			putIf(req, "body", aBody)
			return dispatch(cmd, "ledger-append", req)
		},
	}
	appendCmd.Flags().StringVar(&aStory, "story", "", "story id correlation")
	appendCmd.Flags().StringVar(&aProject, "project", "", "project id correlation")
	appendCmd.Flags().StringVar(&aKind, "kind", "", "entry kind (required)")
	appendCmd.Flags().StringVar(&aActor, "actor", "", "actor")
	appendCmd.Flags().StringVar(&aBody, "body", "", "entry body")
	_ = appendCmd.MarkFlagRequired("kind")

	var lStory, lProject, lKind string
	var lLimit int
	listCmd := &cobra.Command{
		Use:         "list",
		Short:       "List ledger entries (filter by story, project, or kind)",
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{}
			putIf(req, "story_id", lStory)
			putIf(req, "project_id", lProject)
			putIf(req, "kind", lKind)
			if lLimit > 0 {
				req["limit"] = lLimit
			}
			return dispatch(cmd, "ledger-list", req)
		},
	}
	listCmd.Flags().StringVar(&lStory, "story", "", "filter by story id")
	listCmd.Flags().StringVar(&lProject, "project", "", "filter by project id")
	listCmd.Flags().StringVar(&lKind, "kind", "", "filter by kind")
	listCmd.Flags().IntVar(&lLimit, "limit", 0, "max rows (default 200)")

	ledgerCmd.AddCommand(appendCmd, listCmd)
	register(ledgerCmd)
}
