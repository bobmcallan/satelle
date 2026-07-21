package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
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
			if err := dispatch(cmd, group+"-create", req); err != nil {
				return err
			}
			// Story-only: regenerate the disposable backlog view after a successful
			// create so CLI freshness does not depend on serve (sty_d0950127).
			if group == "story" {
				refreshStoryBacklog(cmd)
			}
			return nil
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
	var lStatus, lParent, lTag string
	var lLimit int
	list := &cobra.Command{
		Use:         "list",
		Short:       "List " + plural,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{}
			putIf(req, "status", lStatus)
			putIf(req, "parent_id", lParent)
			putIf(req, "tag", lTag)
			if lLimit > 0 {
				req["limit"] = lLimit
			}
			return dispatch(cmd, group+"-list", req)
		},
	}
	list.Flags().StringVar(&lStatus, "status", "", "filter by status")
	list.Flags().StringVar(&lParent, "parent", "", "filter by parent id")
	list.Flags().StringVar(&lTag, "tag", "", "filter by exact tag (ANY-match in multi-value namespaces)")
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
			// Additive tag mutation (sty_033d4611): never clobbers the rest.
			// Combined with --tags (full replace) is rejected by the verb.
			if f.Changed("add-tags") {
				add, _ := f.GetStringSlice("add-tags")
				req["add_tags"] = add
			}
			if f.Changed("remove-tags") {
				rm, _ := f.GetStringSlice("remove-tags")
				req["remove_tags"] = rm
			}
			if err := dispatch(cmd, group+"-set", req); err != nil {
				return err
			}
			// Story-only: keep the backlog view current after set (sty_d0950127).
			if group == "story" {
				refreshStoryBacklog(cmd)
			}
			return nil
		},
	}
	set.Flags().String("title", "", "new title")
	set.Flags().String("body", "", "new body")
	set.Flags().String("status", "", "new status")
	set.Flags().String("priority", "", "new priority")
	set.Flags().String("category", "", "new category")
	set.Flags().String("parent", "", "new parent id")
	set.Flags().String("acceptance", "", "new acceptance criteria")
	set.Flags().StringSlice("tags", nil, "replace entire tag set (comma-separated); exclusive of --add-tags/--remove-tags")
	set.Flags().StringSlice("add-tags", nil, "add tags without dropping existing ones (comma-separated)")
	set.Flags().StringSlice("remove-tags", nil, "remove tags: exact match, or namespace group (sprint: / sprint:*)")

	parent.AddCommand(create, get, list, set)
	if group == "story" {
		parent.AddCommand(storyDocCommands()...)
		parent.AddCommand(storyCostCommands()...)
		parent.AddCommand(storyDiffCommand())
		parent.AddCommand(storySyncCommand())
		parent.AddCommand(storyRestampCommand())
		parent.AddCommand(storyStopRequestCommand())
		parent.AddCommand(storySeatCommands()...)
	}
	if group == "task" {
		// tasks are authored substrate → `satelle task validate` runs the
		// deterministic task structure check (ACTION+VERIFICATION contract).
		parent.AddCommand(authoredValidateCmd("tasks"))
		parent.AddCommand(taskArchiveCommand())
	}
	if group == "execution" {
		parent.AddCommand(executionRecordCommand())
	}
	return parent
}

// refreshStoryBacklog best-effort regenerates the disposable story-backlog
// view after a story create/set. Synchronous — no goroutine or ticker — so the
// CLI stays self-sufficient without serve (sty_d0950127). A failure never
// fails the mutation command (the DB write already committed); warn on stderr.
func refreshStoryBacklog(cmd *cobra.Command) {
	a, err := appFrom(cmd)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "story: backlog refresh: %v\n", err)
		return
	}
	if _, _, err := verb.SyncStoryBacklog(cmd.Context(), a.Store.Stories, time.Now()); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "story: backlog refresh: %v\n", err)
	}
}

// executionRecordCommand builds `satelle execution record <exe_id>` — the in-loop
// path for collecting a run's OUTPUT as an OKF doc under the parent task's folder
// (sty_890b86cb). The orchestrator calls it as the run's final act (the task
// workflow's exit-gate rubric instructs it to). Output comes from --output or,
// when absent, stdin — so a run's captured log can be piped in.
func executionRecordCommand() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:         "record <exe_id>",
		Short:       "Record a task execution's run output as an OKF doc under its task folder",
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := output
			if !cmd.Flags().Changed("output") {
				if data, err := io.ReadAll(cmd.InOrStdin()); err == nil {
					out = string(data)
				}
			}
			return dispatch(cmd, "execution-record", map[string]any{"id": args[0], "output": out})
		},
	}
	cmd.Flags().StringVar(&output, "output", "", "run output text (default: read from stdin)")
	return cmd
}

// taskArchiveCommand builds `satelle task archive <id>` — a task's disposal path
// (sty_cd209b8a): it marks the store record archived (excluded from the default
// task list, still readable via task get) and MOVES the header + executions to a
// mandatory timestamped backup. Archive is record disposition, distinct from the
// workflow status a task never runs through.
func taskArchiveCommand() *cobra.Command {
	return &cobra.Command{
		Use:         "archive <id>",
		Short:       "Archive a task: move its files to backups and mark the record archived (excluded from list)",
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "task-archive", map[string]any{"id": args[0]})
		},
	}
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

// storyDiffCommand builds `satelle story diff [id]`: enumerate files changed
// since the engagement baseline (sty_da169e03). Enumeration only — no verdict.
// Id may be omitted when stdin is a transition payload `{story:{id},…}` so gate
// functional checks can invoke without shell id plumbing.
func storyDiffCommand() *cobra.Command {
	var patch bool
	cmd := &cobra.Command{
		Use:   "diff [id]",
		Short: "List files changed since engagement baseline (enumeration only)",
		Long: `Report files and a diffstat from the story's engagement baseline
(git HEAD recorded on first entry into a performing state) through the current
worktree, including uncommitted edits and untracked files. Use --patch for the
full unified diff of tracked changes.

No pass/fail: gates (e.g. satelle-story-scope-review) consume this output and
decide. Stories without a baseline (pre-feature or never engaged) error clearly.

Gate functional checks may omit the id and pipe the transition JSON on stdin
({story:{id}, from, to}); the verb reads story.id.`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				req := map[string]any{"id": args[0]}
				if patch {
					req["patch"] = true
				}
				return dispatch(cmd, "story-diff", req)
			}
			// No id: read transition payload (or {id}) from stdin for gate checks.
			in, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return err
			}
			var body map[string]any
			if len(bytes.TrimSpace(in)) > 0 {
				if err := json.Unmarshal(in, &body); err != nil {
					return fmt.Errorf("story diff: stdin JSON: %w", err)
				}
			} else {
				body = map[string]any{}
			}
			if patch {
				body["patch"] = true
			}
			return dispatch(cmd, "story-diff", body)
		},
	}
	cmd.Flags().BoolVar(&patch, "patch", false, "include full unified patch since baseline (tracked)")
	return cmd
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

	// log — the generic typed telemetry/quality event write primitive (AC1,
	// sty_b73c3236), retiring `story step-cost`: any typed event — an in-loop
	// step's self-reported actual tokens/duration and per-step estimate (`--kind
	// step-self-report`), or a future prompted quality signal — is recorded the
	// same way. --data is repeatable; a value that parses as a number is recorded
	// numerically, else as a string. Refused when a key/value looks like a secret.
	var logKind string
	var logData []string
	log := &cobra.Command{
		Use:         "log <id> --kind <kind> [--data key=val ...]",
		Short:       "Record a typed telemetry/quality event against a story",
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, derr := parseLogData(logData)
			if derr != nil {
				return derr
			}
			req := map[string]any{"id": args[0], "kind": logKind}
			if len(data) > 0 {
				req["data"] = data
			}
			return dispatch(cmd, "story-log", req)
		},
	}
	log.Flags().StringVar(&logKind, "kind", "", "the event kind (e.g. step-self-report, agent-retry) — required")
	log.Flags().StringArrayVar(&logData, "data", nil, "a key=value telemetry field; repeatable")

	// cost — the observability VIEW (sty_a699ad14): the per-gate token + wall-time
	// cost recorded on the story's agent_invocation ledger entries, so an operator
	// sees which reviewer/dispatch spent what. Distinct from estimate/actual (the
	// plan's own time/token figures) — this is measured runtime cost.
	cost := &cobra.Command{
		Use:         "cost <id>",
		Short:       "Show the measured per-gate token + wall-time cost recorded for a story",
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := appFrom(cmd); err != nil {
				return err
			}
			sc, err := verb.ComputeStoryCost(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			// Dispatched/reviewed invocations — the precise sub-process cost.
			fmt.Fprintln(w, "TRANSITION\tSTEP\tMODEL\tTOKENS in/out\tTOTAL\tDURATION")
			for _, r := range sc.Rows {
				step := r.Agent
				if r.Skill != "" {
					step = r.Skill
				}
				fmt.Fprintf(w, "%s→%s\t%s\t%s\t%d/%d\t%d\t%s\n",
					r.From, r.To, step, dashIfEmpty(r.Model), r.TokensIn, r.TokensOut, r.TokensTotal, fmtDurationMs(r.DurationMs))
			}
			fmt.Fprintf(w, "TOTAL\t\t\t\t%d\t%s\n", sc.TotalTokens, fmtDurationMs(sc.TotalDurationMs))
			if err := w.Flush(); err != nil {
				return err
			}
			// Per-step report — every step's wall-time (derived from transition
			// timestamps, so IN-LOOP steps are covered too) with any self-reported
			// actual tokens and per-step estimate (satelle story log --kind step-self-report).
			if len(sc.Steps) > 0 {
				sw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(sw, "\nSTEP\tWALL-TIME\tACTUAL TOKENS\tEST TOKENS\tEST TIME")
				for _, s := range sc.Steps {
					fmt.Fprintf(sw, "%s\t%s\t%s\t%s\t%s\n",
						s.Step, fmtDurationMs(s.WallTimeMs),
						stepTokens(s.TokensTotal, s.HasTokens),
						dashIfZero(s.EstTokens), fmtDurationMs(s.EstDurationMs))
				}
				fmt.Fprintf(sw, "TOTAL\t%s\t\t\t\n", fmtDurationMs(sc.TotalWallMs))
				if err := sw.Flush(); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(),
					"note: ACTUAL TOKENS is '—' for an in-loop step until self-reported (satelle story log --kind step-self-report) — the driving session's own tokens aren't measurable by the CLI; '—' means unmeasured, not free.")
			}
			return nil
		},
	}

	// resummarise — re-run the step summariser for one edge to close a missing-
	// summary gap (sty_a1151fb0). The remediation `satelle story cost`/the done-time
	// warning names when a transient kill holed the pull-context chain.
	var rsFrom, rsTo string
	resummarise := &cobra.Command{
		Use:         "resummarise <id> --from <state> --to <state>",
		Short:       "Re-run the step summariser for one edge to close a missing-summary gap",
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-resummarise", map[string]any{"id": args[0], "from": rsFrom, "to": rsTo})
		},
	}
	resummarise.Flags().StringVar(&rsFrom, "from", "", "the edge's FROM state — required")
	resummarise.Flags().StringVar(&rsTo, "to", "", "the edge's TO state — required")

	// retrospect — dispatch the retrospective agent over a finished story to file
	// improvement proposals (sty_b53730e2). Opt-in per story, so its cost (visible
	// via `story cost`) is measured before it is ever made auto-on-done.
	retrospect := &cobra.Command{
		Use:         "retrospect <id>",
		Short:       "Run the retrospective agent over a finished story to file improvement proposals",
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-retrospect", map[string]any{"id": args[0]})
		},
	}

	return []*cobra.Command{estimate, actual, log, cost, resummarise, retrospect}
}

// parseLogData turns a list of "key=value" flags into a typed data map: a
// value that parses as an int64 or a float64 is recorded numerically, else as
// a string — so a caller passes `--data tokens_total=42000` and gets a JSON
// number, not a quoted string, in the telemetry payload.
func parseLogData(kv []string) (map[string]any, error) {
	if len(kv) == 0 {
		return nil, nil
	}
	data := make(map[string]any, len(kv))
	for _, pair := range kv {
		key, val, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid --data %q: want key=value", pair)
		}
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			data[key] = n
			continue
		}
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			data[key] = f
			continue
		}
		data[key] = val
	}
	return data, nil
}

// stepTokens renders an in-loop step's self-reported actual tokens. A step with no
// recorded tokens shows '—' (unmeasured — the CLI can't see the driving session's
// tokens), deliberately distinct from a recorded 0.
func stepTokens(total int, has bool) string {
	if !has {
		return "—"
	}
	return strconv.Itoa(total)
}

// dashIfZero renders a count, or '—' when zero (no estimate recorded).
func dashIfZero(n int) string {
	if n <= 0 {
		return "—"
	}
	return strconv.Itoa(n)
}

// fmtDurationMs renders a millisecond duration compactly (e.g. 3.1s, 2m4s). Zero
// (an uninstrumented/plain-text invocation) renders as a dash.
func fmtDurationMs(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return (time.Duration(ms) * time.Millisecond).Round(100 * time.Millisecond).String()
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
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

	// lessons — cross-story enumeration of typed lessons/lesson attachments
	// (offline friction corpus; never session-injected).
	lessons := &cobra.Command{
		Use:         "lessons",
		Short:       "List typed lessons artifacts across all stories",
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-lessons-list", map[string]any{})
		},
	}
	return []*cobra.Command{attach, docs, doc, lessons}
}

// storyStopRequestCommand builds `satelle story stop-request <id> --reason …`
// (sty_8426b9c0 AC5). Annotates the engagement lease; never kills a process.
func storyStopRequestCommand() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:         "stop-request <id>",
		Short:       "Request stop of another agent's engaged story (arbitrated at next step edge)",
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-stop-request", map[string]any{"id": args[0], "reason": reason})
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "why the stop is requested")
	return cmd
}

// storySeatCommands builds `satelle story seat` (list) and
// `satelle story seat release <id>` — the agent/operator surface for the
// engagement seat (sty_1738f973 AC4).
func storySeatCommands() []*cobra.Command {
	seat := &cobra.Command{
		Use:         "seat",
		Short:       "List engagement seat leases (reaps stale rows first)",
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-seat-list", map[string]any{})
		},
	}
	release := &cobra.Command{
		Use:         "release <id>",
		Short:       "Force-release an engagement seat by story/task id",
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-seat-release", map[string]any{"id": args[0]})
		},
	}
	seat.AddCommand(release)
	return []*cobra.Command{seat}
}
