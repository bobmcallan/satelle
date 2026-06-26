package cli

import (
	"github.com/spf13/cobra"
)

// Story and task are the same primitive; their command groups are built by one
// factory so the surface stays identical. Verb names follow the architecture's
// standard: list/get (read) + create/set (write), all kebab-case.
func init() {
	register(workItemGroup("story", "stories", "Manage stories (units of work / goals)"))
	register(workItemGroup("task", "tasks", "Manage tasks (project-level to-dos)"))
}

// workItemGroup builds a `satelle <group>` command with create/get/list/set
// subcommands dispatching to the <group>-* verbs. plural is used only in help
// text (e.g. "List stories").
func workItemGroup(group, plural, short string) *cobra.Command {
	parent := &cobra.Command{Use: group, Short: short}

	// create
	var cTitle, cBody, cStatus, cPriority, cCategory, cParent, cAccept string
	var cTags []string
	create := &cobra.Command{
		Use:         "create",
		Short:       "Create a " + group,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"title": cTitle}
			putIf(req, "body", cBody)
			putIf(req, "status", cStatus)
			putIf(req, "priority", cPriority)
			putIf(req, "category", cCategory)
			putIf(req, "parent_id", cParent)
			putIf(req, "acceptance_criteria", cAccept)
			if len(cTags) > 0 {
				req["tags"] = cTags
			}
			return dispatch(cmd, group+"-create", req)
		},
	}
	create.Flags().StringVar(&cTitle, "title", "", "title (required)")
	create.Flags().StringVar(&cBody, "body", "", "body / description")
	create.Flags().StringVar(&cStatus, "status", "", "status (default open)")
	create.Flags().StringVar(&cPriority, "priority", "", "priority")
	create.Flags().StringVar(&cCategory, "category", "", "category")
	create.Flags().StringVar(&cParent, "parent", "", "parent item id")
	create.Flags().StringVar(&cAccept, "acceptance", "", "acceptance criteria")
	create.Flags().StringSliceVar(&cTags, "tags", nil, "comma-separated tags")
	_ = create.MarkFlagRequired("title")

	// get
	get := &cobra.Command{
		Use:         "get <id>",
		Short:       "Get a " + group + " by id",
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, group+"-get", map[string]any{"id": args[0]})
		},
	}

	// list
	var lStatus, lParent string
	var lLimit int
	list := &cobra.Command{
		Use:         "list",
		Short:       "List " + plural,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{}
			putIf(req, "status", lStatus)
			putIf(req, "parent_id", lParent)
			if lLimit > 0 {
				req["limit"] = lLimit
			}
			return dispatch(cmd, group+"-list", req)
		},
	}
	list.Flags().StringVar(&lStatus, "status", "", "filter by status")
	list.Flags().StringVar(&lParent, "parent", "", "filter by parent id")
	list.Flags().IntVar(&lLimit, "limit", 0, "max rows (default 500)")

	// set (partial update — only flags the user changed are sent)
	set := &cobra.Command{
		Use:         "set <id>",
		Short:       "Update a " + group + " (only the flags you pass change)",
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"id": args[0]}
			f := cmd.Flags()
			putChanged(req, f, "title", "title")
			putChanged(req, f, "body", "body")
			putChanged(req, f, "status", "status")
			putChanged(req, f, "priority", "priority")
			putChanged(req, f, "category", "category")
			putChanged(req, f, "parent", "parent_id")
			putChanged(req, f, "acceptance", "acceptance_criteria")
			if f.Changed("tags") {
				tags, _ := f.GetStringSlice("tags")
				req["tags"] = tags
			}
			return dispatch(cmd, group+"-set", req)
		},
	}
	set.Flags().String("title", "", "new title")
	set.Flags().String("body", "", "new body")
	set.Flags().String("status", "", "new status")
	set.Flags().String("priority", "", "new priority")
	set.Flags().String("category", "", "new category")
	set.Flags().String("parent", "", "new parent id")
	set.Flags().String("acceptance", "", "new acceptance criteria")
	set.Flags().StringSlice("tags", nil, "replace tags (comma-separated)")

	parent.AddCommand(create, get, list, set)
	return parent
}
