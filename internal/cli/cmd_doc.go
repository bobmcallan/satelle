package cli

import (
	"github.com/spf13/cobra"
)

func init() {
	docCmd := &cobra.Command{Use: "doc", Short: "Read indexed authored docs (markdown source-of-truth)",
		Long: `Read the authored markdown satelle has indexed — documents, principles, skills,
workflows, tasks.

Read-only, and the FILE is the source of truth: the index is a derived view, so
a doc that looks stale here is a reindex away, not an edit. To change one, edit
the markdown and run satelle reindex.`}

	var lKind string
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List indexed authored docs (optionally by kind)",
		Long: `List the authored docs in the index, optionally narrowed to one kind
(documents, principles, skills, workflows, tasks).

Reach for it to find what a repo already authored before writing something that
exists. It lists what was INDEXED, so a file added since the last reindex is not
here yet.`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{}
			putIf(req, "kind", lKind)
			return dispatch(cmd, "doc-list", req)
		},
	}
	listCmd.Flags().StringVar(&lKind, "kind", "", "filter by kind (documents|workflows|principles|skills)")

	getCmd := &cobra.Command{
		Use:   "get <kind> <name>",
		Short: "Get one indexed authored doc by kind and name",
		Long: `Print one authored doc's body by kind and name — the fastest way to read a
principle or skill without knowing where it lives on disk.

Name is the doc's NAME, not a path. The same name resolves whether the file is
repo-authored or an embedded default, and the repo's copy wins.`,
		Args:        cobra.ExactArgs(2),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "doc-get", map[string]any{"kind": args[0], "name": args[1]})
		},
	}

	docCmd.AddCommand(listCmd, getCmd)
	register(docCmd)
}
