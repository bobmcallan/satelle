package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	register(&cobra.Command{
		Use:         "status",
		Short:       "Show the local repo's config, database, and store counts",
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			stories, err := a.Store.Stories.Count(ctx, workitem.KindStory)
			if err != nil {
				return err
			}
			tasks, err := a.Store.Stories.Count(ctx, workitem.KindTask)
			if err != nil {
				return err
			}
			events, err := a.Store.Ledger.Count(ctx)
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "repo root\t%s\n", a.RepoRoot)
			fmt.Fprintf(w, "database\t%s\n", a.DBPath)
			fmt.Fprintf(w, "web port\t%d\n", a.Config.ResolveWebPort())
			fmt.Fprintf(w, "log level\t%s\n", a.Config.ResolveLogLevel())
			fmt.Fprintf(w, "stories\t%d\n", stories)
			fmt.Fprintf(w, "tasks\t%d\n", tasks)
			fmt.Fprintf(w, "ledger entries\t%d\n", events)

			dirs := a.AuthoredDirs()
			for _, kind := range config.AuthoredKinds {
				n, err := a.Store.DocIndex.Count(ctx, kind)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "indexed %s\t%d  (%s)\n", kind, n, dirs[kind])
			}
			return w.Flush()
		},
	})
}
