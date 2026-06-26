package cli

import (
	"github.com/spf13/cobra"
)

func init() {
	register(&cobra.Command{
		Use:   "index",
		Short: "Sync authored markdown (documents, workflows, principles, skills) into the index",
		Long: `index runs the directory monitor once: it walks the configured authored
dirs, (re)indexes changed markdown files, and prunes entries whose files were
removed. Run it to refresh the index on demand; the web server runs the same
sync continuously.`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			// The CLI supplies the resolved authored dirs; the verb runs the
			// sync, so CLI and web refresh the index through the same path.
			return dispatch(cmd, "doc-sync", map[string]any{"dirs": a.AuthoredDirs()})
		},
	})
}
