package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/verb"
)

// Story and task are the same primitive; their command groups are built by one
// factory so the surface stays identical. Verb names follow the architecture's
// standard: list/get (read) + create/set (write), all kebab-case.
func init() {
	register(workItemGroup("story", "stories", "Manage stories (units of work / goals)"))
	register(workItemGroup("task", "tasks", "Manage tasks (project-level to-dos)"))
	// An execution is an isolated RUN of a task (create with --parent <tsk_id>);
	// it carries the run lifecycle while the task header stays a stable definition
	// (sty_ef08ce2a).
	register(workItemGroup("execution", "executions", "Manage task executions (isolated runs of a task)"))
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
	create.Flags().StringVar(&cStatus, "status", "", "status (default backlog)")
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
	if group == "story" {
		parent.AddCommand(storyDocCommands()...)
		parent.AddCommand(storyCostCommands()...)
		parent.AddCommand(storySyncCommand())
		parent.AddCommand(storyRestampCommand())
	}
	if group == "task" {
		// tasks are authored substrate → `satelle task validate` runs the
		// deterministic task structure check (ACTION+VERIFICATION contract).
		parent.AddCommand(authoredValidateCmd("tasks"))
	}
	return parent
}

// attachBody resolves the document body for `story attach`: --file reads it
// from a file (sty_97c53d72 — a multi-KB summary should not shell-quote through
// a flag), otherwise --body is used verbatim. The flags are declared mutually
// exclusive; a read failure is surfaced with the path context.
func attachBody(body, file string) (string, error) {
	if file == "" {
		return body, nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("attach: read --file: %w", err)
	}
	return string(data), nil
}

// storySyncCommand builds `satelle story sync` (sty_8f7b2157): the dedicated,
// inspectable reconciliation of .satelle/stories — backlog-only views + an
// artifact review that REPORTS orphans/misfiles (never deletes evidence).
func storySyncCommand() *cobra.Command {
	return &cobra.Command{
		Use:         "sync",
		Short:       "Reconcile .satelle/stories: backlog-only views; review artifact dirs against the DB",
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			rep, err := verb.SyncStories(cmd.Context(), a.Store.Stories, time.Now())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "stories: %d backlog view(s) materialized; %d pruned; %d artifact dir(s)\n", rep.Materialized, rep.Pruned, rep.ArtifactDirs)
			for _, id := range rep.Orphaned {
				fmt.Fprintf(out, "  ORPHANED %s/ — no story in the database (authored evidence; remove manually if unwanted)\n", id)
			}
			for _, p := range rep.Problems {
				fmt.Fprintf(out, "  PROBLEM  %s\n", p)
			}
			if len(rep.Orphaned) == 0 && len(rep.Problems) == 0 {
				fmt.Fprintln(out, "artifacts: clean")
			}
			return nil
		},
	}
}

// storyCostCommands builds `satelle story estimate` and `satelle story actual`:
// the agent records a plan estimate at begin-work and the actual cost at close.
// Each dispatches to the story-estimate / story-actual verb, which writes the
// estimate-*/actual-* tags and a ledger row.
func storyCostCommands() []*cobra.Command {
	var eTime, eBasis string
	var eTokens int
	estimate := &cobra.Command{
		Use:         "estimate <id>",
		Short:       "Record a story's plan estimate (time/tokens)",
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"id": args[0]}
			putIf(req, "time", eTime)
			putIf(req, "basis", eBasis)
			if eTokens > 0 {
				req["tokens"] = eTokens
			}
			return dispatch(cmd, "story-estimate", req)
		},
	}
	estimate.Flags().StringVar(&eTime, "time", "", "estimated duration (e.g. 30m, 2h)")
	estimate.Flags().IntVar(&eTokens, "tokens", 0, "estimated tokens")
	estimate.Flags().StringVar(&eBasis, "basis", "", "optional note on the estimate basis")

	var aTime string
	var aTokens int
	actual := &cobra.Command{
		Use:         "actual <id>",
		Short:       "Record a story's actual cost (time/tokens)",
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"id": args[0]}
			putIf(req, "time", aTime)
			if aTokens > 0 {
				req["tokens"] = aTokens
			}
			return dispatch(cmd, "story-actual", req)
		},
	}
	actual.Flags().StringVar(&aTime, "time", "", "actual duration (e.g. 50m)")
	actual.Flags().IntVar(&aTokens, "tokens", 0, "actual tokens")

	return []*cobra.Command{estimate, actual}
}

// storyRestampCommand builds `satelle story restamp <id> [--workflow <name>]`:
// the first-class re-stamp of a story's governing workflow (sty_ed3386cf) — the
// sanctioned replacement for hand-editing the tag list when the right workflow
// changes mid-flight (a re-categorised story, or a more specific category
// workflow authored after create). Dispatches to the story-restamp verb, which
// validates the target (it must resolve, and the story's current status must be
// one of its states), upserts the workflow: tag preserving every other tag, and
// records a workflow_stamped ledger row plus an operation-log line.
func storyRestampCommand() *cobra.Command {
	var wfName string
	restamp := &cobra.Command{
		Use:   "restamp <id>",
		Short: "Re-stamp the story's governing workflow (re-resolve by category, or --workflow)",
		Long: `restamp re-stamps the workflow that governs a story. Without --workflow it
re-resolves from the story's CURRENT category — the same resolution create uses —
so a re-categorised story picks up its category-specific workflow. With
--workflow <name> it stamps that workflow explicitly.

The target is validated before anything changes: the workflow must resolve in
the substrate, and the story's current status must be a state the workflow
declares (else the story would be stranded mid-lifecycle). Every other tag —
estimate/actual, category, ad-hoc — survives untouched, and the change is
recorded as a workflow_stamped ledger row and an operation-log line. Stories
only: tasks and executions are unstamped by design.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"id": args[0]}
			putIf(req, "workflow", wfName)
			return dispatch(cmd, "story-restamp", req)
		},
	}
	restamp.Flags().StringVar(&wfName, "workflow", "", "explicit workflow to stamp (default: re-resolve from the story's category)")
	return restamp
}

// storyDocCommands builds the per-story document attachment surface: attach a
// typed markdown doc, list a story's docs, and read one. They dispatch to the
// story-doc-* verbs, which store each doc as portable markdown beside the story.
func storyDocCommands() []*cobra.Command {
	var aName, aType, aBody, aFile string
	attach := &cobra.Command{
		Use:         "attach <id>",
		Short:       "Attach a typed markdown document to a story",
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := attachBody(aBody, aFile)
			if err != nil {
				return err
			}
			req := map[string]any{"story_id": args[0]}
			putIf(req, "name", aName)
			putIf(req, "type", aType)
			putIf(req, "body", body)
			return dispatch(cmd, "story-doc-attach", req)
		},
	}
	attach.Flags().StringVar(&aName, "name", "", "document name (required)")
	attach.Flags().StringVar(&aType, "type", "", "document type (plan|change|output|…)")
	attach.Flags().StringVar(&aBody, "body", "", "document markdown body")
	attach.Flags().StringVar(&aFile, "file", "", "read the document body from a file (alternative to --body)")
	attach.MarkFlagsMutuallyExclusive("body", "file")
	_ = attach.MarkFlagRequired("name")

	docs := &cobra.Command{
		Use:         "docs <id>",
		Short:       "List a story's attached documents",
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-doc-list", map[string]any{"story_id": args[0]})
		},
	}

	doc := &cobra.Command{
		Use:         "doc <id> <name>",
		Short:       "Read one of a story's attached documents",
		Args:        cobra.ExactArgs(2),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-doc-get", map[string]any{"story_id": args[0], "name": args[1]})
		},
	}
	return []*cobra.Command{attach, docs, doc}
}
