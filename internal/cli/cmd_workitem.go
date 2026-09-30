package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
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
	parent := &cobra.Command{
		Use: group, Short: short, Long: groupParentLong(group),
		// A group with no subcommand shows its help; an unknown subcommand (a
		// removed one such as `story chat`) is refused rather than silently
		// printing help and exiting 0 (sty_6f9ba7ca).
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
		},
	}

	// create
	var cTitle, cBody, cStatus, cPriority, cCategory, cParent, cAccept string
	var cTags []string
	create := &cobra.Command{
		Use:         "create",
		Short:       "Create a " + group,
		Long:        groupCreateLong(group),
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
			// Surface the pre-seed ungated state (sty_d4d0ee59): when gate_create
			// is neither on nor explicitly opted out, tell the operator on the
			// create path itself. Never fail a successful create over the advisory.
			// Category warn-mode advisory (sty_b2315e17) rides the same channel.
			if a, aerr := appFrom(cmd); aerr == nil {
				if notice := createGateNotice(a.Config.Review.GateCreate, a.DataDir); notice != "" {
					fmt.Fprint(cmd.ErrOrStderr(), notice)
				}
				if notice := categoryNotice(a.Config, cCategory); notice != "" {
					fmt.Fprint(cmd.ErrOrStderr(), notice)
				}
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
		Long:        groupGetLong(group),
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
		Long:        groupListLong(group, plural),
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
		Long:        groupSetLong(group),
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"id": args[0]}
			f := cmd.Flags()
			// Step-edge self-report nudge (sty_56aae77a AC3): capture previous
			// status before the transition so the advisory names the step just left.
			var prevStatus string
			if group == "story" && f.Changed("status") {
				if a, aerr := appFrom(cmd); aerr == nil && a != nil {
					if it, gerr := a.Store.Stories.Get(cmd.Context(), args[0]); gerr == nil {
						prevStatus = it.Status
					}
				}
			}
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
			// Category warn-mode advisory on an explicit --category change only
			// (sty_b2315e17): a status-only set on a legacy category never trips it.
			if f.Changed("category") {
				if a, aerr := appFrom(cmd); aerr == nil {
					cat, _ := f.GetString("category")
					if notice := categoryNotice(a.Config, cat); notice != "" {
						fmt.Fprint(cmd.ErrOrStderr(), notice)
					}
				}
			}
			// Step-edge self-report nudge after a real status change (sty_56aae77a).
			// No hardcoded terminal-status list — suppress only when status did not
			// change or a self-report for the left step already exists.
			if group == "story" && f.Changed("status") && prevStatus != "" {
				newStatus, _ := f.GetString("status")
				if newStatus != "" && newStatus != prevStatus {
					already := verb.HasStepSelfReport(cmd.Context(), args[0], prevStatus)
					if note := stepSelfReportNudge(args[0], prevStatus, already); note != "" {
						fmt.Fprint(cmd.ErrOrStderr(), note)
					}
				}
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
		parent.AddCommand(storyProofCommand())
		parent.AddCommand(storyDefinitionEditsCommand())
		parent.AddCommand(storySyncCommand())
		parent.AddCommand(storyReconcileCommand())
		parent.AddCommand(storyRestampCommand())
		parent.AddCommand(storyAmendCommand())
		parent.AddCommand(storyStopRequestCommand())
		parent.AddCommand(storyRecoverCommand())
		parent.AddCommand(storySeatCommands()...)
		parent.AddCommand(storyHoldCommands())
		parent.AddCommand(storyTidyCommands()...)
		parent.AddCommand(storyMessageCommand())
		parent.AddCommand(storyMessagesCommand())
		parent.AddCommand(storyReworkCommand())
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
		Use:   "record <exe_id>",
		Short: "Record a task execution's run output as an OKF doc under its task folder",
		Long: `Record what a run produced, as an authored document under the parent task's
folder — the evidence half of an execution.

--output takes the text; with the flag absent it reads STDIN, so a captured log
pipes straight in. Reach for it before closing the run: the after-validator
judges what the run did, and an unrecorded run leaves it nothing to read.`,
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
		Use:   "archive <id>",
		Short: "Archive a task: move its files to backups and mark the record archived (excluded from list)",
		Long: `Retire a superseded task: the record is marked archived (dropped from the
default task list, still readable via task get) and its header plus executions
MOVE to a timestamped backup directory.

Archive is record DISPOSITION, not a workflow status — a task runs no lifecycle
to close. The files move rather than vanish, so a mistaken archive is
recoverable from the backup tree.`,
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
//
// `--file -` reads STDIN (sty_ef8a896b): piping evidence is what an agent
// reaches for, and the bare os.ReadFile failed on the conventional sentinel with
// a confusing "open -: no such file". in is the command's own reader
// (cmd.InOrStdin()), the same idiom `execution record` uses, so a test can
// inject one.
func attachBody(in io.Reader, body, file string) (string, error) {
	if file == "" {
		return body, nil
	}
	if file == "-" {
		data, err := io.ReadAll(in)
		if err != nil {
			return "", fmt.Errorf(`attach: read --file - (stdin): %w`, err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("attach: read --file: %w", err)
	}
	return string(data), nil
}

// attachBinary reads a local binary file, base64-encodes it, and dispatches
// story-doc-attach-binary. Cap, allowlist, and sniff live in the verb
// (sty_40e5a305); the CLI only defaults content-type from the extension when
// --content-type is omitted.
func attachBinary(cmd *cobra.Command, storyID, name, typ, path, contentType string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("attach: read --binary-file: %w", err)
	}
	if contentType == "" {
		// Convenience default only — verb still sniffs and cross-checks.
		if nameExt := filepath.Ext(name); nameExt != "" {
			contentType = mime.TypeByExtension(nameExt)
		}
		if contentType == "" {
			contentType = mime.TypeByExtension(filepath.Ext(path))
		}
	}
	req := map[string]any{
		"story_id":    storyID,
		"name":        name,
		"data_base64": base64.StdEncoding.EncodeToString(data),
	}
	putIf(req, "type", typ)
	putIf(req, "content_type", contentType)
	return dispatch(cmd, "story-doc-attach-binary", req)
}

// docWriteOut fetches a document and writes its body to path. Binary attachments
// are decoded from base64; markdown is written as the attachment body string.
// Never streams raw binary to stdout (sty_40e5a305 AC6).
func docWriteOut(cmd *cobra.Command, storyID, name, outPath string, force bool) error {
	if !force {
		if _, err := os.Stat(outPath); err == nil {
			return fmt.Errorf("doc: %s already exists (pass --force to overwrite)", outPath)
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("doc: stat --out: %w", err)
		}
	}
	// Probe list metadata via get-binary first when name looks binary; else markdown.
	// Prefer binary get when the name has a non-.md extension.
	ext := strings.ToLower(filepath.Ext(name))
	if ext != "" && ext != ".md" {
		return docWriteOutBinary(cmd, storyID, name, outPath)
	}
	// Markdown (or name without extension — try markdown get).
	var body json.RawMessage
	b, err := json.Marshal(map[string]any{"story_id": storyID, "name": name})
	if err != nil {
		return err
	}
	body, err = verb.Dispatch(cmd.Context(), "story-doc-get", b)
	if err != nil {
		// If markdown get failed because it is binary, fall through to binary path.
		if strings.Contains(err.Error(), "binary attachment") {
			return docWriteOutBinary(cmd, storyID, name, outPath)
		}
		return err
	}
	var ref struct {
		Body string `json:"body"`
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &ref); err != nil {
		return fmt.Errorf("doc: decode: %w", err)
	}
	if err := os.WriteFile(outPath, []byte(ref.Body), 0o644); err != nil {
		return fmt.Errorf("doc: write --out: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d bytes, type=%s)\n", outPath, len(ref.Body), ref.Type)
	return nil
}

func docWriteOutBinary(cmd *cobra.Command, storyID, name, outPath string) error {
	b, err := json.Marshal(map[string]any{"story_id": storyID, "name": name})
	if err != nil {
		return err
	}
	raw, err := verb.Dispatch(cmd.Context(), "story-doc-get-binary", b)
	if err != nil {
		return err
	}
	var ref struct {
		Name        string `json:"name"`
		ContentType string `json:"content_type"`
		Size        int64  `json:"size"`
		SHA256      string `json:"sha256"`
		DataB64     string `json:"data_base64"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		return fmt.Errorf("doc: decode binary: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(ref.DataB64)
	if err != nil {
		return fmt.Errorf("doc: decode base64: %w", err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return fmt.Errorf("doc: write --out: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d bytes, %s, sha256:%s)\n", outPath, len(data), ref.ContentType, ref.SHA256)
	return nil
}

// storySyncCommand builds `satelle story sync` (sty_8f7b2157): the dedicated,
// inspectable reconciliation of .satelle/stories — backlog-only views + an
// artifact review that REPORTS orphans/misfiles (never deletes evidence).
func storySyncCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Reconcile .satelle/stories: backlog-only views; review artifact dirs against the DB",
		Long: `Reconcile the on-disk .satelle/stories tree against the database: rematerialise
the backlog views and review the per-story artifact directories.

Reach for it when the views look stale or an artifact directory is unaccounted
for. It REPORTS orphans and problems and never deletes evidence — an ORPHANED
directory is authored material with no story row, and removing it is your call,
not this command's.`,
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

// storyReconcileCommand builds `satelle story reconcile [--repair] [--json]`.
// Default is a dry-run report of row/ledger status drift; --repair applies.
func storyReconcileCommand() *cobra.Command {
	var repair, asJSON bool
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Detect (and optionally repair) story rows whose status disagrees with the ledger",
		Long: `Compare each work-item row's status to its last status_transition ledger
event. The ledger is the source of truth: a recorded transition whose 'to'
does not match the row is drift.

Default is a dry-run report. --repair sets each drifted row from the ledger
and records a status_reconcile event. Dry-run exits non-zero when drift is
found so the check is scriptable.`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			if repair {
				repaired, rerr := verb.RepairStatusDrift(ctx, a.Store.Stories, a.Store.Ledger, time.Now())
				if rerr != nil {
					return rerr
				}
				if asJSON {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					return enc.Encode(map[string]any{"repaired": repaired, "count": len(repaired)})
				}
				if len(repaired) == 0 {
					fmt.Fprintln(out, "status: clean — no row disagrees with its last status_transition")
					return nil
				}
				fmt.Fprintf(out, "repaired %d stor(y/ies) from the ledger:\n", len(repaired))
				for _, d := range repaired {
					fmt.Fprintf(out, "  %s  %s → %s\n", d.ID, d.RowStatus, d.LedgerStatus)
				}
				return nil
			}
			drifts, derr := verb.DetectStatusDrift(ctx, a.Store.Stories, a.Store.Ledger)
			if derr != nil {
				return derr
			}
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(map[string]any{"drift": drifts, "count": len(drifts)}); err != nil {
					return err
				}
			} else if len(drifts) == 0 {
				fmt.Fprintln(out, "status: clean — no row disagrees with its last status_transition")
			} else {
				fmt.Fprintf(out, "%d stor(y/ies) disagree with the ledger:\n", len(drifts))
				for _, d := range drifts {
					fmt.Fprintf(out, "  %s  row=%s ledger=%s  (last transition %s)\n",
						d.ID, d.RowStatus, d.LedgerStatus, d.TransitionAt.UTC().Format(time.RFC3339))
				}
				fmt.Fprintln(out, "re-run with --repair to set each row from the ledger")
			}
			if len(drifts) > 0 {
				return fmt.Errorf("story reconcile: %d drifted", len(drifts))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&repair, "repair", false, "set drifted rows from the last status_transition")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON")
	return cmd
}

// storyDiffCommand builds `satelle story diff [id]`: enumerate files changed
// since the engagement baseline (sty_da169e03). Enumeration only — no verdict.
// Id may be omitted when stdin is a transition payload `{story:{id},…}` so gate
// functional checks can invoke without shell id plumbing.
func storyDiffCommand() *cobra.Command {
	var patch, recorded, includeSubstrate, full bool
	cmd := &cobra.Command{
		Use:   "diff [id]",
		Short: "List files changed since engagement baseline (enumeration only)",
		Long: `Enumerate the files a story changed since its engagement baseline (the git
HEAD recorded on first entry into a performing state), including uncommitted
and untracked ones. Reach for it when a gate — or you — must know the slice.


Three enumerations, and picking the wrong one is the usual mistake:
  default              live git re-derive, baseline → worktree (--patch for the diff)
  --recorded           the change_record rows satelle wrote at each transition —
                       what a gate needs, and it sees git-ignored substrate too
  --include-substrate  also unions mtime-changed authored substrate, opt-in

It decides nothing; gates consume the output and judge. The diff is anchored to
the tree the story was engaged from and refuses elsewhere.`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				req := map[string]any{"id": args[0]}
				if patch {
					req["patch"] = true
				}
				if recorded {
					req["recorded"] = true
				}
				if includeSubstrate {
					req["include_substrate"] = true
				}
				return dispatch(cmd, "story-diff", req)
			}
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
			if recorded {
				body["recorded"] = true
			}
			if includeSubstrate {
				body["include_substrate"] = true
			}
			return dispatch(cmd, "story-diff", body)
		},
	}
	cmd.Flags().BoolVar(&patch, "patch", false, "include full unified patch since baseline (tracked)")
	cmd.Flags().BoolVar(&recorded, "recorded", false, "union change_record file lists instead of live git re-derive (sty_948ad5df)")
	cmd.Flags().BoolVar(&includeSubstrate, "include-substrate", false, "opt-in: union substrate mtime leg (authored dirs + data dir); default live path stays git-only")
	cmd.Flags().BoolVar(&full, "full", false, "skip compact rendering (noise-strip and ranking) and print the raw patch (sty_918e2086)")
	return cmd
}

// storyProofCommand builds `satelle story proof [id]`: enumerate tests added or
// changed since the engagement baseline (sty_76796b8e). Enumeration only — it
// never runs tests and never exits non-zero for an enumerable state (including
// no-baseline and no new tests). Stdin-tolerant like story diff.
func storyProofCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proof [id]",
		Short: "List tests changed since engagement baseline (enumeration only)",
		Long: `Enumerate the tests a story added or changed since its engagement baseline.
JSON only. Report-only — it does not run tests and does not decide pass/fail.

Reach for it from a functional-check gate (pipe the transition payload, no argv
id) or as an operator inspecting the slice. Default isolated reviewers have no
shell; this command is not injected into their payload.

Enumerable states always exit 0: no baseline (state=no_baseline), no new tests
(tests=[]), unparseable languages (listed under skipped with a reason), foreign
tree, git unavailable. Non-zero only for unknown id or a non-story item.

Go _test.go files include parsed func Test* names. Other languages are listed
by path with skipped reason. Changed non-test paths appear in non_test_files
so an empty tests list is not confused with a classifier miss.`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return dispatch(cmd, "story-proof", map[string]any{"id": args[0]})
			}
			in, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return err
			}
			var body map[string]any
			if len(bytes.TrimSpace(in)) > 0 {
				if err := json.Unmarshal(in, &body); err != nil {
					return fmt.Errorf("story proof: stdin JSON: %w", err)
				}
			} else {
				body = map[string]any{}
			}
			return dispatch(cmd, "story-proof", body)
		},
	}
	return cmd
}

// storyDefinitionEditsCommand builds `satelle story definition-edits <id>`:
// enumerate the edits made to a story's definition while the route left it
// editable (sty_5262592e). Report-only — a functional-check gate reads the JSON
// and decides what a non-empty list means; the command itself always exits 0 for
// a readable story.
func storyDefinitionEditsCommand() *cobra.Command {
	var sinceEdge string
	cmd := &cobra.Command{
		Use:   "definition-edits <id>",
		Short: "List a story's definition edits (enumeration only)",
		Long: `List the title/body/acceptance_criteria/category edits recorded on a story's
ledger while its definition was still editable — field, before, after, actor.
JSON only. Report-only: it decides nothing.

--since-edge <from>:<to> keeps only the edits made after the story's latest
accepted <from>→<to> transition (for example backlog:plan). A functional-check
gate uses it to notice a definition edited after readiness was accepted, without
compiling that rule into the binary. With no accepted edge there is nothing to
have gone stale and the list is empty.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"id": args[0]}
			if sinceEdge != "" {
				req["since_edge"] = sinceEdge
			}
			return dispatch(cmd, "story-definition-edits", req)
		},
	}
	cmd.Flags().StringVar(&sinceEdge, "since-edge", "", "only edits after the latest accepted <from>:<to> transition (e.g. backlog:plan)")
	return cmd
}

// storyCostCommands builds `satelle story estimate` and `satelle story actual`:
// the driving session records a plan estimate at begin-work; the actual is
// COMPUTED, never entered (sty_8eae81ac AC1). Each dispatches to the
// story-estimate / story-actual verb.
func storyCostCommands() []*cobra.Command {
	var eTime, eBasis string
	var eTokens, eFreshInput, eOutput int
	var eUSD float64
	estimate := &cobra.Command{
		Use:   "estimate <id>",
		Short: "Record a story's plan estimate (usd, fresh-input/output, or legacy tokens/time)",
		Long: `Record the plan's estimate for a story: --usd for dollars, --fresh-input/
--output for tokens — the CURRENT units. --tokens and --time are LEGACY units,
kept readable and writable for stories already written in them, but never
converted into usd or fresh input.

The driving session records it — not the planner — and a workflow that gates on
the estimate greps those TAGS, not a plan section, so a figure written only into
the plan artifact leaves the gate unsatisfied. Record it before requesting the
transition the gate fires on.

--time takes a duration (30m, 2h, 1h30m) or a bare number of MINUTES (38).`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"id": args[0]}
			putIf(req, "time", eTime)
			putIf(req, "basis", eBasis)
			if eTokens > 0 {
				req["tokens"] = eTokens
			}
			if eUSD > 0 {
				req["usd"] = eUSD
			}
			if eFreshInput > 0 {
				req["fresh_input"] = eFreshInput
			}
			if eOutput > 0 {
				req["output"] = eOutput
			}
			return dispatch(cmd, "story-estimate", req)
		},
	}
	estimate.Flags().Float64Var(&eUSD, "usd", 0, "estimated dollars")
	estimate.Flags().IntVar(&eFreshInput, "fresh-input", 0, "estimated fresh input tokens")
	estimate.Flags().IntVar(&eOutput, "output", 0, "estimated output tokens")
	estimate.Flags().StringVar(&eTime, "time", "", "legacy unit: estimated duration (30m, 2h, or a bare number of minutes)")
	estimate.Flags().IntVar(&eTokens, "tokens", 0, "legacy unit: a bare estimated token count (never usd or fresh input)")
	estimate.Flags().StringVar(&eBasis, "basis", "", "optional note on the estimate basis")

	actual := &cobra.Command{
		Use:   "actual <id>",
		Short: "Compute and record a story's actual cost from its ledger",
		Long: `Compute the story's actual from its ledger (dispatch + driver-usage rows) and
record it as actual-usd / actual-fresh-input / actual-output / actual-cache-read
/ actual-cache-write / actual-minutes / actual-tokens tags, plus the full
actual_recorded ledger payload.

The actual is COMPUTED, never entered: this command takes no figures — pass
none. A hand-typed actual-* tag is overwritten by the computed one. This is
also run automatically when the story reaches its workflow's terminal state, so
running it by hand is for an early look, not a requirement.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-actual", map[string]any{"id": args[0]})
		},
	}

	// log — the generic typed telemetry/quality event write primitive (AC1,
	// sty_b73c3236), retiring `story step-cost`: any typed event — an in-loop
	// step's self-reported actual tokens/duration and per-step estimate (`--kind
	// step-self-report`), or a future prompted quality signal — is recorded the
	// same way. --data is repeatable; a value that parses as a number is recorded
	// numerically, else as a string. Refused when a key/value looks like a secret.
	var logKind string
	var logData []string
	log := &cobra.Command{
		Use:   "log <id> --kind <kind> [--data key=val ...]",
		Short: "Record a typed telemetry/quality event against a story",
		Long: `Append a typed event to a story's ledger — a step self-report, a quality
signal, whatever the kind names.

--data takes key=value pairs and types them: a value that parses as a number is
stored numerically, everything else as a string, so --data tokens_total=42000
lands as a number a later reader can sum. Append-only: nothing here rewrites a
prior entry.`,
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
	var bySkill, allStories bool
	var skillStoryID, sessionID string
	var gateValue bool
	var gateEpicID, gateSince, gateUntil string
	var gateJSON bool
	cost := &cobra.Command{
		Use:   "cost [id]",
		Short: "Show the measured per-gate token + wall-time cost recorded for a story",
		Long: `Show what a story actually cost to run: dollars and fresh tokens headline,
cache read/write as secondary columns, elapsed wall time and agent time, any
legacy plan estimate beside the measured figure it matches, the driving
session's own line, and — for an epic-parent — the family roll-up. Below that,
the per-gate token + wall-time detail taken from the agent-invocation ledger
entries, plus a per-step roll-up.

This is MEASURED transport cost, distinct from the estimate/actual tags. A row
printed as "—" is unmeasured, never free.

--by-skill rolls up agent_invocation rows by skill (--all or --story <id>).
--session <id> lists every story that session drove, plus its remainder.
--gate-value reports invocations/dollars/fresh tokens/accepts/rejects/$-per-
reject per skill and seat, scoped by --story, --epic, or --since/--until.`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := appFrom(cmd); err != nil {
				return err
			}
			if gateValue {
				return runGateValue(cmd, gateValueArgs{
					storyID: skillStoryID, epicID: gateEpicID,
					since: gateSince, until: gateUntil, asJSON: gateJSON,
				})
			}
			if sessionID != "" {
				recon, err := verb.ComputeSessionReconciliation(cmd.Context(), sessionID)
				if err != nil {
					return err
				}
				printSessionReconciliation(cmd, recon)
				return nil
			}
			if bySkill {
				scope := skillStoryID
				if !allStories && scope == "" {
					if len(args) == 1 {
						scope = args[0]
					} else {
						return fmt.Errorf("story cost --by-skill: pass --all, --story <id>, or a story id")
					}
				} else if allStories && (scope != "" || len(args) == 1) {
					return fmt.Errorf("story cost --by-skill: --all and --story/<id> are mutually exclusive")
				}
				rollup, err := verb.ComputeSkillRollup(cmd.Context(), scope)
				if err != nil {
					return err
				}
				rw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(rw, "SKILL\tINVOCATIONS\tFRESH\tCACHE-WRITE\tCACHE-READ\tUNSPLIT\tTOTAL IN\tTOTAL OUT\tAVG SYS BYTES\tAVG PAYLOAD BYTES\t$")
				for _, r := range rollup.Rows {
					fmt.Fprintf(rw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
						r.Skill, r.Invocations, r.FreshTokens, r.CacheWriteTokens, r.CacheReadTokens,
						r.UnsplitTokens, r.TotalInputTokens, r.TotalOutputTokens,
						r.AvgSystemPromptBytes, r.AvgPayloadBytes,
						costTotalLabel(r.TotalCostUSD, r.Invocations-r.UncostedRows, r.UncostedRows))
				}
				if err := rw.Flush(); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(),
					"note: UNSPLIT is measured input from rows recorded before the cache split existed — add it to FRESH+CACHE-WRITE+CACHE-READ to reconcile TOTAL IN.")
				return nil
			}
			if len(args) != 1 {
				return fmt.Errorf("story cost: a story id is required (or pass --by-skill)")
			}
			// A gate wait the harness has since flushed is closed here, by the read
			// that wants its count (sty_c4b92c9e).
			verb.SettleGateWaits(cmd.Context(), args[0], time.Now().UTC())
			sc, err := verb.ComputeStoryCost(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			printCostSummary(cmd, sc)
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			// Dispatched/reviewed invocations — the precise sub-process cost.
			// Unreported usage renders as — (sty_56aae77a), never a confident 0.
			fmt.Fprintln(w, "TRANSITION\tSTEP\tMODEL\tTOKENS in/out\tTOTAL\tDURATION\t$")
			for _, r := range sc.Rows {
				step := r.Agent
				if r.Skill != "" {
					step = r.Skill
				}
				fmt.Fprintf(w, "%s→%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					r.From, r.To, step, ledger.ModelLabel(r.Model, r.ModelResolved),
					rowTokensIO(r.TokensIn, r.TokensOut, r.UsageAvailable),
					rowTokensTotal(r.TokensTotal, r.UsageAvailable),
					fmtDurationMs(r.DurationMs),
					rowCostUSD(r.CostUSD))
			}
			fmt.Fprintf(w, "TOTAL\t\t\t\t%s\t%s\t%s\n",
				measuredTotalLabel(sc.TotalTokens, sc.MeasuredRows, sc.UnmeasuredRows),
				fmtDurationMs(sc.TotalDurationMs),
				costTotalLabel(sc.TotalCostUSD, sc.CostedRows, sc.UncostedRows))
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
			}
			// Driver section (sty_81caa41b): the driving session's own recorded
			// usage, kept separate from the dispatched/reviewer rows above.
			if len(sc.DriverRows) > 0 {
				if err := printDriverSection(cmd, sc); err != nil {
					return err
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(),
				"note: '—' means unmeasured, never free. Dispatched rows without provider usage are unknown; in-loop ACTUAL TOKENS need satelle story log --kind step-self-report. actual-* tags are computed from the ledger by `satelle story actual` (never hand-entered); step-self-report figures remain session self-report, not measured transport cost. TOKENS in includes cache-creation and cache-read tokens when the provider reports them (the full prompt, not the uncached remainder); rows recorded before that accounting omit cache and understate input.")
			return nil
		},
	}
	cost.Flags().BoolVar(&bySkill, "by-skill", false, "roll up agent_invocation rows by skill instead of by story (needs --all or --story)")
	cost.Flags().BoolVar(&allStories, "all", false, "with --by-skill, roll up across every story")
	cost.Flags().StringVar(&skillStoryID, "story", "", "with --by-skill or --gate-value, scope to one story only")
	cost.Flags().StringVar(&sessionID, "session", "", "list every story this driving session drove, plus its unattributed remainder")
	cost.Flags().BoolVar(&gateValue, "gate-value", false, "report invocations/dollars/fresh tokens/accepts/rejects/$-per-reject per skill and seat")
	cost.Flags().StringVar(&gateEpicID, "epic", "", "with --gate-value, scope to an epic-parent and every descendant found by parent_id")
	cost.Flags().StringVar(&gateSince, "since", "", "with --gate-value, only entries at/after this date (YYYY-MM-DD)")
	cost.Flags().StringVar(&gateUntil, "until", "", "with --gate-value, only entries at/before this date (YYYY-MM-DD)")
	cost.Flags().BoolVar(&gateJSON, "json", false, "with --gate-value, emit the GateValueReport as JSON")

	// resummarise — re-run the step summariser for one edge to close a missing-
	// summary gap (sty_a1151fb0). The remediation `satelle story cost`/the done-time
	// warning names when a transient kill holed the pull-context chain.
	var rsFrom, rsTo string
	resummarise := &cobra.Command{
		Use:   "resummarise <id> --from <state> --to <state>",
		Short: "Re-run the step summariser for one edge to close a missing-summary gap",
		Long: `Re-run the step summariser for ONE edge whose summary is missing — the hole a
transient kill leaves in the trail.

Both --from and --to are required: this repairs a named edge, not the story.
Reach for it when the route or a done-time warning says a summary is absent;
it fills the gap rather than re-judging anything.`,
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
		Use:   "retrospect <id>",
		Short: "Run the retrospective agent over a finished story to file improvement proposals",
		Long: `Dispatch the retrospective agent over a FINISHED story: it reads what happened
and files improvement proposals.

Opt-in per story, deliberately — it costs a full agent turn, visible afterwards
in satelle story cost. Reach for it on a story worth learning from, not as a
routine close step.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			model, _ := cmd.Flags().GetString("model")
			return dispatch(cmd, "story-retrospect", map[string]any{"id": args[0], "model": model})
		},
	}
	retrospect.Flags().String("model", "", "model for this dispatch, overriding agents.toml (recorded with source=agent)")

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

// rowTokensIO renders TOKENS in/out for a dispatched row. Unreported usage is
// '—/—' — never a confident 0/0 (sty_56aae77a).
func rowTokensIO(in, out int, available bool) string {
	if !available {
		return "—/—"
	}
	return fmt.Sprintf("%d/%d", in, out)
}

// rowTokensTotal renders TOTAL for a dispatched row. Unreported → '—'.
func rowTokensTotal(total int, available bool) string {
	if !available {
		return "—"
	}
	return strconv.Itoa(total)
}

// measuredTotalLabel renders the TOTAL column for the cost table. When some
// invocations are unreported, the count is inline so the number is never
// mistaken for a full-story total (sty_56aae77a). When NOTHING was measured
// (measured == 0), totalTokens is the sum of an empty set, not a real zero —
// printing it bare would misrepresent "nothing measured" as "measured zero"
// (sty_b8542a3a AC1 rework), so this renders "unavailable" instead, same as
// the rest of the cost view's never-implied-zero convention.
func measuredTotalLabel(totalTokens, measured, unmeasured int) string {
	if measured == 0 {
		if unmeasured > 0 {
			return fmt.Sprintf("unavailable (%d invocations unreported)", unmeasured)
		}
		return "unavailable"
	}
	if unmeasured <= 0 {
		return strconv.Itoa(totalTokens)
	}
	return fmt.Sprintf("%d (measured; %d of %d invocations unreported)",
		totalTokens, unmeasured, measured+unmeasured)
}

// rowCostUSD renders a row's $ column. Nil means the provider reported no
// cost — "unknown", never a confident $0 (sty_c4df7376).
func rowCostUSD(cost *float64) string {
	if cost == nil {
		return "unknown"
	}
	return fmt.Sprintf("$%.4f", *cost)
}

// costTotalLabel renders a TOTAL/rollup $ figure. costed==0 means nothing in
// the set priced at all — unknown, never $0. A nonzero uncosted count prints
// inline so the total is never mistaken for the full set's cost.
func costTotalLabel(total float64, costed, uncosted int) string {
	if costed == 0 {
		return "unknown"
	}
	if uncosted == 0 {
		return fmt.Sprintf("$%.4f", total)
	}
	return fmt.Sprintf("$%.4f (%d rows unknown)", total, uncosted)
}

// driverRowsToCostview converts verb's richer DriverUsagePayload rows (the
// driver_usage ledger payload, which also carries fields printDriverSection
// has no use for — WindowKey, Cumulative, Late/Pending — owned by the
// recorder in driver_usage.go) down to costview.DriverRow: the figures-only
// shape costview.FormatDriverRows renders, so the CLI table and the web
// page's Driver sessions table are built from the same rows through the
// same formatting pass and can never disagree (sty_b8542a3a AC7 rework).
func driverRowsToCostview(rows []verb.DriverUsagePayload) []costview.DriverRow {
	out := make([]costview.DriverRow, len(rows))
	for i, d := range rows {
		out[i] = costview.DriverRow{
			SessionID: d.SessionID, Executable: d.Executable, Model: d.Model,
			FreshInput: d.FreshInput, CacheRead: d.CacheRead, CacheWrite: d.CacheWrite, Output: d.Output,
			CostUSD: d.CostUSD, CostUnavailableReason: d.CostUnavailableReason,
			Available: d.Available, WallSeconds: d.WallSeconds, Trigger: d.Trigger,
			From: d.From, To: d.To,
			ModelCalls: d.ModelCalls, ModelCallsUnavailableReason: d.ModelCallsUnavailableReason,
			Turns: d.Turns, Unflushed: d.Unflushed,
		}
		out[i].Cumulative.ModelCalls = d.Cumulative.ModelCalls
	}
	return out
}

// printDriverSection renders the DRIVER SESSION table — the driving (in-loop)
// session's own recorded usage, kept separate from the dispatched/reviewer
// Rows above (sty_81caa41b, sty_b8542a3a AC1/AC7 rework). costview.
// FormatDriverRows is the SAME formatting pass the web page's Driver sessions
// table calls: fresh input, output, cache read and cache write are separate
// columns, never folded into one cache-inclusive figure, and a TOTAL column
// with nothing measured at all renders "unavailable" rather than a literal 0.
func printDriverSection(cmd *cobra.Command, sc verb.StoryCost) error {
	rowViews, totalView := costview.FormatDriverRows(driverRowsToCostview(sc.DriverRows))
	dw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(dw, "\nDRIVER SESSION\tEXECUTABLE\tTRIGGER\tFRESH IN\tOUT\tCALLS\tCACHE READ\tCACHE WRITE\t$\tAGENT TIME")
	for _, d := range rowViews {
		fmt.Fprintf(dw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			d.SessionID, d.Executable, d.Trigger, d.FreshIn, d.Out, d.Calls, d.CacheRead, d.CacheWrite, d.USD, d.AgentTime)
	}
	if totalView != nil {
		fmt.Fprintf(dw, "%s\t\t\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			totalView.SessionID, totalView.FreshIn, totalView.Out, totalView.Calls, totalView.CacheRead, totalView.CacheWrite,
			totalView.USD, totalView.AgentTime)
	}
	if err := dw.Flush(); err != nil {
		return err
	}
	printGateWaits(cmd, sc.GateWaits)
	// GRAND TOTAL reads off sc.Figures — costview.Own's single accumulation
	// over BOTH agent_invocation and driver_usage rows (figures.go addDispatchRow/
	// addDriverRow feed the same accumulator) — rather than the legacy
	// GrandTotalTokens/GrandTotalCostUSD fields (a second, cache-inclusive sum
	// kept only for JSON back-compat). Fresh/out/cache stay separate columns
	// and never blend into one cache-inclusive figure, and "unavailable" never
	// reads as a literal 0 (sty_b8542a3a AC1 rework).
	f := sc.Figures
	splitRows := f.UsageRows - f.UnsplitRows
	fmt.Fprintf(cmd.OutOrStdout(), "\nGRAND TOTAL (agent_invocation + driver): %s | fresh in %s | out %s | cache read %s | cache write %s | agent time %s\n",
		costview.FormatUSD(f.CostUSD, f.CostRows, f.CostUnavailableRows),
		costview.FormatSplitTokens(f.FreshInput, splitRows, f.UnsplitRows, f.UsageUnavailableRows),
		costview.FormatTokensMeasured(f.Output, f.UsageRows, f.UsageUnavailableRows),
		costview.FormatSplitTokens(f.CacheRead, splitRows, f.UnsplitRows, f.UsageUnavailableRows),
		costview.FormatSplitTokens(f.CacheWrite, splitRows, f.UnsplitRows, f.UsageUnavailableRows),
		costview.FormatDuration(f.AgentMs))
	for _, recon := range sc.Sessions {
		printSessionReconciliation(cmd, recon)
	}
	return nil
}

// printGateWaits renders the driver's model calls across each handed-off gate
// wait (sty_c4b92c9e): the count read from the gate rows, or why there is none
// yet — open while the harness has not flushed the turn, "—" where it cannot say.
func printGateWaits(cmd *cobra.Command, waits []costview.GateWait) {
	if len(waits) == 0 {
		return
	}
	gw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(gw, "\nGATE WAIT\tEXECUTABLE\tDRIVER MODEL CALLS\tNOTE")
	for _, w := range waits {
		fmt.Fprintf(gw, "%s\t%s\t%s\t%s\n", w.Handle, w.Executable, costview.FormatModelCalls(w), w.Reason)
	}
	_ = gw.Flush()
}

// printSessionReconciliation renders one driving session's reconciliation
// (sty_81caa41b AC9/AC11): every story it drove plus the unattributed
// remainder — session time the ledger cannot pin to any story (e.g. between
// one story's close and the next story's engage).
func printSessionReconciliation(cmd *cobra.Command, recon verb.SessionReconciliation) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintf(cmd.OutOrStdout(), "\nSession %s (%s) reconciliation:\n", recon.SessionID, recon.Executable)
	fmt.Fprintln(w, "STORY\tTOKENS\t$")
	for _, s := range recon.Stories {
		tokens, cost := "—", "unknown"
		if s.Available {
			tokens = strconv.Itoa(s.FreshInput + s.CacheRead + s.CacheWrite + s.Output)
			cost = rowCostUSD(s.CostUSD)
		} else {
			tokens = fmt.Sprintf("unavailable (%s)", s.UnavailableReason)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.StoryID, tokens, cost)
	}
	total := "unavailable"
	if recon.TotalAvailable {
		total = strconv.Itoa(recon.Total.FreshInput + recon.Total.CacheRead + recon.Total.CacheWrite + recon.Total.Output)
	} else if recon.TotalUnavailableReason != "" {
		total = fmt.Sprintf("unavailable (%s)", recon.TotalUnavailableReason)
	}
	unattributed := "partial (an attributed story is unavailable)"
	if recon.UnattributedAvailable {
		unattributed = strconv.Itoa(recon.Unattributed.FreshInput + recon.Unattributed.CacheRead + recon.Unattributed.CacheWrite + recon.Unattributed.Output)
	}
	fmt.Fprintf(w, "SESSION TOTAL\t%s\t\nUNATTRIBUTED\t%s\t\n", total, unattributed)
	_ = w.Flush()
}

// printCostSummary renders the costview headline (sty_b8542a3a AC1-AC4): $
// and fresh tokens lead, cache read/write are secondary columns, elapsed wall
// time and agent time are both shown (never substituted for each other), any
// legacy estimate prints in its own unit beside the figure it matches, and —
// when the story has children — the family roll-up (AC5). It is the SAME
// costview.Figures the actual-* tags and the web page read, so all three
// agree on every number.
func printCostSummary(cmd *cobra.Command, sc verb.StoryCost) {
	out := cmd.OutOrStdout()
	f := sc.Figures
	fmt.Fprintln(out, "COST SUMMARY")
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	splitRows := f.UsageRows - f.UnsplitRows
	fmt.Fprintf(w, "$\t%s\t%s\n", costview.FormatUSD(f.CostUSD, f.CostRows, f.CostUnavailableRows), costview.FormatEstimate(sc.Estimates, "usd"))
	fmt.Fprintf(w, "FRESH IN\t%s\t%s\n", costview.FormatSplitTokens(f.FreshInput, splitRows, f.UnsplitRows, f.UsageUnavailableRows), costview.FormatEstimate(sc.Estimates, "fresh-input"))
	fmt.Fprintf(w, "OUT\t%s\t%s\n", costview.FormatTokensMeasured(f.Output, f.UsageRows, f.UsageUnavailableRows), costview.FormatEstimate(sc.Estimates, "output"))
	fmt.Fprintf(w, "CACHE READ\t%s\t\n", costview.FormatSplitTokens(f.CacheRead, splitRows, f.UnsplitRows, f.UsageUnavailableRows))
	fmt.Fprintf(w, "CACHE WRITE\t%s\t\n", costview.FormatSplitTokens(f.CacheWrite, splitRows, f.UnsplitRows, f.UsageUnavailableRows))
	if f.UnsplitTokens > 0 {
		fmt.Fprintf(w, "UNSPLIT (legacy)\t%s\t\n", costview.FormatTokens(f.UnsplitTokens))
	}
	fmt.Fprintf(w, "elapsed (wall)\t%s\t%s\n", costview.FormatDuration(f.ElapsedMs), costview.FormatEstimate(sc.Estimates, "minutes"))
	fmt.Fprintf(w, "agent time (dispatch+driver)\t%s\t\n", costview.FormatDuration(f.AgentMs))
	_ = w.Flush()
	if legacy := costview.FormatLegacyTokenEstimate(sc.Estimates); legacy != "" {
		fmt.Fprintln(out, legacy)
	}
	if sc.Family != nil {
		fmt.Fprintln(out, "\nFAMILY")
		fw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(fw, "CHILD\t$\tFRESH IN\tOUT\tCACHE READ\tCACHE WRITE\tELAPSED")
		for _, c := range sc.Family.Children {
			cf := c.Figures
			cfSplitRows := cf.UsageRows - cf.UnsplitRows
			fmt.Fprintf(fw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", c.ID,
				costview.FormatUSD(cf.CostUSD, cf.CostRows, cf.CostUnavailableRows),
				costview.FormatSplitTokens(cf.FreshInput, cfSplitRows, cf.UnsplitRows, cf.UsageUnavailableRows), costview.FormatTokensMeasured(cf.Output, cf.UsageRows, cf.UsageUnavailableRows),
				costview.FormatSplitTokens(cf.CacheRead, cfSplitRows, cf.UnsplitRows, cf.UsageUnavailableRows), costview.FormatSplitTokens(cf.CacheWrite, cfSplitRows, cf.UnsplitRows, cf.UsageUnavailableRows),
				costview.FormatDuration(cf.ElapsedMs))
		}
		ft := sc.Family.Total
		ftSplitRows := ft.UsageRows - ft.UnsplitRows
		fmt.Fprintf(fw, "FAMILY TOTAL\t%s\t%s\t%s\t%s\t%s\t%s\n",
			costview.FormatUSD(ft.CostUSD, ft.CostRows, ft.CostUnavailableRows),
			costview.FormatSplitTokens(ft.FreshInput, ftSplitRows, ft.UnsplitRows, ft.UsageUnavailableRows), costview.FormatTokensMeasured(ft.Output, ft.UsageRows, ft.UsageUnavailableRows),
			costview.FormatSplitTokens(ft.CacheRead, ftSplitRows, ft.UnsplitRows, ft.UsageUnavailableRows), costview.FormatSplitTokens(ft.CacheWrite, ftSplitRows, ft.UnsplitRows, ft.UsageUnavailableRows),
			costview.FormatDuration(ft.ElapsedMs))
		_ = fw.Flush()
	}
	fmt.Fprintln(out)
}

// gateValueArgs is the parsed input to runGateValue — one of storyID or
// epicID scopes the story set (storyID wins if both are set); since/until
// (either may be empty) bound the date range.
type gateValueArgs struct {
	storyID, epicID string
	since, until    string
	asJSON          bool
}

// runGateValue renders `satelle story cost --gate-value` (sty_b8542a3a AC6):
// invocations, dollars, fresh tokens, accepts, rejects and dollars-per-reject
// per skill/seat. It is a read-only query over stored evidence — it
// recommends nothing and changes no configuration.
func runGateValue(cmd *cobra.Command, args gateValueArgs) error {
	opts := verb.GateValueOptions{StoryID: args.storyID, EpicID: args.epicID}
	if args.since != "" {
		t, err := time.Parse("2006-01-02", args.since)
		if err != nil {
			return fmt.Errorf("story cost --gate-value: --since: %w", err)
		}
		opts.Since = t
	}
	if args.until != "" {
		t, err := time.Parse("2006-01-02", args.until)
		if err != nil {
			return fmt.Errorf("story cost --gate-value: --until: %w", err)
		}
		opts.Until = t.Add(24*time.Hour - time.Nanosecond) // inclusive of the whole day
	}
	report, err := verb.ComputeGateValue(cmd.Context(), opts)
	if err != nil {
		return err
	}
	if args.asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	// A bundled reviewer session is one measured call judging several rubrics;
	// each rubric's row carries an ALLOCATED share of it, and the NOTE column
	// says so (sty_23e10d92). The column only appears when a row carries one.
	allocated := false
	for _, r := range report.Rows {
		allocated = allocated || r.AllocationNote != ""
	}
	header := "SKILL\tSEAT\tINVOCATIONS\t$\tFRESH TOKENS\tUNSPLIT (legacy)\tACCEPTS\tREJECTS\t$/REJECT"
	if allocated {
		header += "\tNOTE"
	}
	fmt.Fprintln(w, header)
	for _, r := range report.Rows {
		splitRows := r.UsageRows - r.UnsplitRows
		line := fmt.Sprintf("%s\t%s\t%d\t%s\t%s\t%s\t%d\t%d\t%s",
			r.Skill, r.Seat, r.Invocations,
			costview.FormatUSD(r.CostUSD, r.Costed, r.Uncosted), costview.FormatSplitTokens(r.FreshTokens, splitRows, r.UnsplitRows, r.UsageUnavailableRows),
			dashIfZero(r.UnsplitTokens),
			r.Accepts, r.Rejects, costview.FormatCostPerReject(r.CostUSD, r.Costed, r.Uncosted, r.Rejects))
		if allocated {
			line += "\t" + r.AllocationNote
			if r.AllocationUnavailableReason != "" {
				line += " (unavailable: " + r.AllocationUnavailableReason + ")"
			}
		}
		fmt.Fprintln(w, line)
	}
	return w.Flush()
}

// stepSelfReportNudge is the step-edge advisory printed after a real status
// change (sty_56aae77a AC3). Empty when suppressed (already reported). Pure so
// the CLI test can pin the wording without a full transition.
func stepSelfReportNudge(storyID, prevStatus string, alreadyReported bool) string {
	if storyID == "" || prevStatus == "" || alreadyReported {
		return ""
	}
	return fmt.Sprintf(
		"note: record the step you just finished —\n  satelle story log %s --kind step-self-report --data step=%s --data tokens_total=<n>\n",
		storyID, prevStatus)
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

// storyAmendCommand builds `satelle story amend <id>` — the gated correction of
// a story's FROZEN definition fields (sty_81aa4d8f). `story set` refuses those
// fields once a story leaves its entry state, and before this the only exit from
// a wrong definition was cancel-and-re-raise: a new id and a full route re-walk.
// Amend keeps the id and the history, and pays for that with a gate: the repo's
// amend_review reviewer judges the correction, the reason is mandatory, and the
// ledger records every field's before and after.
func storyAmendCommand() *cobra.Command {
	var mTitle, mBody, mAccept, mCategory, mReason string
	amend := &cobra.Command{
		Use:   "amend <id>",
		Short: "Amend a story's frozen definition fields under the amend gate (title/body/acceptance/category)",
		Long: `Correct a wrong definition on a story that has already left its entry state.

title, body, acceptance_criteria and category freeze there, so an agent cannot
weaken its own acceptance criteria to make a gate pass. amend does not lift that
freeze — it makes the correction judged: --reason is mandatory, the repo's
amend_review reviewer accepts or rejects it, a reject changes nothing, and an
accepted change records each field's old and new value on the ledger. Declare no
amend_review hook and there is no amend.

Status never moves. amend runs from a performing state and from the parked
(blocked) state, so a story parked over a wrong definition is corrected and
resumed in place; terminal and cancelled are refused. --body and --acceptance
take "-" to read stdin.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			req := map[string]any{"id": args[0]}
			putChanged(req, f, "title", "title")
			putChanged(req, f, "category", "category")
			// body/acceptance may be piped: a corrected AC list is exactly the
			// multi-line value that should not shell-quote through a flag.
			for _, m := range []struct{ flag, key string }{
				{"body", "body"}, {"acceptance", "acceptance_criteria"},
			} {
				if !f.Changed(m.flag) {
					continue
				}
				v, _ := f.GetString(m.flag)
				if v == "-" {
					data, err := io.ReadAll(cmd.InOrStdin())
					if err != nil {
						return fmt.Errorf("amend: read --%s - (stdin): %w", m.flag, err)
					}
					v = string(data)
				}
				req[m.key] = v
			}
			putIf(req, "reason", mReason)
			return dispatch(cmd, "story-amend", req)
		},
	}
	amend.Flags().StringVar(&mTitle, "title", "", "corrected title")
	amend.Flags().StringVar(&mBody, "body", "", `corrected body, or "-" to read it from stdin`)
	amend.Flags().StringVar(&mAccept, "acceptance", "", `corrected acceptance criteria, or "-" to read them from stdin`)
	amend.Flags().StringVar(&mCategory, "category", "", "corrected category (re-lanes the route; refused if the new lane has no such state)")
	amend.Flags().StringVar(&mReason, "reason", "", "why the definition was wrong (required; recorded and judged)")
	_ = amend.MarkFlagRequired("reason")
	return amend
}

// storyDocCommands builds the per-story document attachment surface: attach a
// typed markdown or binary doc, list a story's docs, and read one. Markdown
// dispatches to story-doc-*; binary uses story-doc-attach-binary / get-binary
// with sidecar metadata (sty_40e5a305). Multimodal gate judgment is out of scope.
func storyDocCommands() []*cobra.Command {
	var aName, aType, aBody, aFile, aBinaryFile, aContentType string
	attach := &cobra.Command{
		Use:   "attach <id>",
		Short: "Attach a typed document (markdown or binary) to a story",
		Long: `Attach a typed document to a story.

Markdown (--body / --file) is stored with frontmatter beside the story. Use
--file - to pipe the body in on STDIN, which is how a generated report or a
heredoc of evidence reaches the store without shell-quoting it through --body.
Binary (--binary-file) is stored byte-for-byte with a .satelle.json sidecar;
size cap and content-type allowlist are enforced in the verb (SVG/HTML denied
as executable if served). Gates receive a reference only — multimodal judgment
of binary content is out of scope.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The root command tree is registered once (init → register); flag
			// variables persist across invocations. Always key off Changed, never
			// leftover string values from a prior call.
			bodySet := cmd.Flags().Changed("body")
			fileSet := cmd.Flags().Changed("file")
			binSet := cmd.Flags().Changed("binary-file")
			if (bodySet && fileSet) || (bodySet && binSet) || (fileSet && binSet) {
				return fmt.Errorf("attach: --body, --file, and --binary-file are mutually exclusive")
			}
			if binSet {
				ct := ""
				if cmd.Flags().Changed("content-type") {
					ct = aContentType
				}
				return attachBinary(cmd, args[0], aName, aType, aBinaryFile, ct)
			}
			if !bodySet && !fileSet {
				// Name the valid inputs rather than attach an empty document
				// (sty_ef8a896b): an agent that reached for an unsupported input
				// flag otherwise gets a silent empty attachment, not a fix.
				return fmt.Errorf(`attach: one of --body, --file (use "-" for stdin), or --binary-file is required`)
			}
			bodyIn, fileIn := "", ""
			if bodySet {
				bodyIn = aBody
			}
			if fileSet {
				fileIn = aFile
			}
			body, err := attachBody(cmd.InOrStdin(), bodyIn, fileIn)
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
	attach.Flags().StringVar(&aName, "name", "", "document name (required; binary keeps its extension)")
	attach.Flags().StringVar(&aType, "type", "", "document type (plan|change|output|screenshot|…)")
	attach.Flags().StringVar(&aBody, "body", "", "document markdown body")
	attach.Flags().StringVar(&aFile, "file", "", `read the document body from a file, or "-" for stdin (alternative to --body)`)
	attach.Flags().StringVar(&aBinaryFile, "binary-file", "", "attach a binary file (PNG/PDF/…); mutually exclusive with --body/--file")
	attach.Flags().StringVar(&aContentType, "content-type", "", "MIME type for --binary-file (defaulted from extension when omitted; verb still sniffs)")
	attach.MarkFlagsMutuallyExclusive("body", "file")
	_ = attach.MarkFlagRequired("name")

	docs := &cobra.Command{
		Use:   "docs <id>",
		Short: "List a story's attached documents",
		Long: `List the documents attached to a story — plan, route, change records, step
summaries, and whatever evidence was attached by hand.

Names and types only; read one with satelle story doc <id> <name>. Reach for it
when a gate asks for evidence you are not sure exists: the attachment NAME is
what a presence check looks for.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-doc-list", map[string]any{"story_id": args[0]})
		},
	}

	var docOut string
	var docForce bool
	doc := &cobra.Command{
		Use:   "doc <id> <name>",
		Short: "Read one of a story's attached documents",
		Long: `Read one attached document.

Markdown bodies print as JSON on stdout. Binary attachments never stream raw
bytes to stdout: pass --out <path> to write the file (refuse overwrite unless
--force), or the command fails with guidance.`,
		Args:        cobra.ExactArgs(2),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if docOut != "" {
				return docWriteOut(cmd, args[0], args[1], docOut, docForce)
			}
			return dispatch(cmd, "story-doc-get", map[string]any{"story_id": args[0], "name": args[1]})
		},
	}
	doc.Flags().StringVar(&docOut, "out", "", "write the document body to this path (required for binary)")
	doc.Flags().BoolVar(&docForce, "force", false, "overwrite --out if the path already exists")

	// route — the story's route AND the reasoning behind every outcome, as one
	// artifact (sty_39e2d9df). Renders the attached document when the story has
	// moved; renders the route live from the governing workflow when it has not,
	// so "what is my route" is answerable from backlog. Read-only.
	route := &cobra.Command{
		Use:   "route <id>",
		Short: "Show a story's route — its ordered steps and the reasoning behind every outcome so far",
		Long: `route renders the single artifact a story carries about its own process.

The plan half is the ordered route: each step, the obligation it discharges, who
performs it under which rubrics, and the reviewers gating entry — including which
gates are present only because the story carries a tag.

The outcome half is appended as steps resolve: each reviewer's verdict, its
reasoning, and a pointer to the full output in the ledger.

Read-only, and answerable without opening any workflow file.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := appFrom(cmd); err != nil {
				return err
			}
			body, err := verb.StoryRoute(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), strings.TrimRight(body, "\n"))
			return nil
		},
	}

	// lessons — cross-story enumeration of typed lessons/lesson attachments
	// (offline friction corpus; never session-injected).
	lessons := &cobra.Command{
		Use:   "lessons",
		Short: "List typed lessons artifacts across all stories",
		Long: `List every typed lessons artifact across all stories — the accumulated friction
corpus, in one place.

Read it when you want the record of what went wrong before, deliberately: these
artifacts are never session-injected, so nothing surfaces them unless you ask.`,
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-lessons-list", map[string]any{})
		},
	}
	return []*cobra.Command{attach, docs, doc, route, lessons}
}

// storyStopRequestCommand builds `satelle story stop-request <id> --reason …`
// (sty_8426b9c0 AC5; sty_7b69954a names this as the LIVE-seat preemption path).
// Annotates the engagement lease; never kills a process.
func storyStopRequestCommand() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "stop-request <id>",
		Short: "Request stop of another agent's engaged story (LIVE seat preemption; arbitrated at next step edge)",
		Long: `Request that another agent stop advancing an engaged story so the engagement seat can free.

This is the preemption path for a LIVE seat held by another agent — not for a stale seat
(use "satelle story seat release <id>" for that). The holder is refused forward engaging
moves at the next step edge and may park (blocked) or terminate; parking frees the seat.
Never cancel a healthy story to free the seat — cancelled is terminal.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-stop-request", map[string]any{"id": args[0], "reason": reason})
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "why the stop is requested")
	return cmd
}

// storyRecoverCommand builds `satelle story recover <id> [--choice …]`
// (sty_f0ed2473): after a dispatch ends without a completion, report what it
// left; with --choice, record the driver's decision as one ledger row.
func storyRecoverCommand() *cobra.Command {
	var choice, reason string
	cmd := &cobra.Command{
		Use:   "recover <id>",
		Short: "Report what a dispatch that ended without a completion left; record the recovery choice",
		Long: `Report what a dispatch that ended without a completion left behind: its last
recorded event and the wall time since, and the files present since the
engagement anchor. Reach for it when a dispatch stalled, died or was reaped and
you must decide what to do next.

It REPORTS; it does not VOUCH. The transition did not commit and the work is
unverified; the file list is an enumeration, not a coherence check. Not a
resume: no live session is re-entered.

--choice redispatch|finish|park records your decision as one recovery_choice
ledger row and stops. It never dispatches, transitions or parks.

See: satelle help agent-dispatch`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"id": args[0]}
			putIf(req, "choice", choice)
			putIf(req, "reason", reason)
			resp, err := verb.Dispatch(cmd.Context(), "story-recover", mustJSON(req))
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return printJSON(cmd, resp)
			}
			var res struct {
				Report string `json:"report"`
			}
			if err := json.Unmarshal(resp, &res); err != nil {
				return printJSON(cmd, resp)
			}
			fmt.Fprint(cmd.OutOrStdout(), res.Report)
			return nil
		},
	}
	cmd.Flags().StringVar(&choice, "choice", "", "record the recovery decision: redispatch, finish or park")
	cmd.Flags().StringVar(&reason, "reason", "", "why this choice (kept on the ledger row)")
	return cmd
}

// storySeatCommands builds `satelle story seat` (list) and
// `satelle story seat release <id>` — the agent/operator surface for the
// engagement seat (sty_1738f973 AC4).
func storySeatCommands() []*cobra.Command {
	seat := &cobra.Command{
		Use:   "seat",
		Short: "List engagement seat leases (reaps stale rows first)",
		Long: `List the engagement seat leases: who holds one, from which working tree, and how
old the heartbeat is. Stale rows are reaped before the list is rendered, so what
you see is live.

Reach for it when an engagement is refused. The heartbeat age is the reading
that matters: a LIVE holder is another agent still working, and the path is
stop-request; a STALE one is nobody, and the path is seat release.`,
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-seat-list", map[string]any{})
		},
	}
	release := &cobra.Command{
		Use:   "release <id>",
		Short: "Force-release an engagement seat by story/task id",
		Long: `Force-release an engagement seat by the id holding it.

For a STALE seat — no live agent behind it, which the heartbeat age in satelle
story seat shows. Releasing a LIVE holder's seat abandons work another agent
believes it is still doing: ask for it with stop-request instead, which lets the
holder park itself. Never cancel a healthy story to free a seat.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "story-seat-release", map[string]any{"id": args[0]})
		},
	}
	seat.AddCommand(release)
	return []*cobra.Command{seat}
}

// storyMessageCommand is `satelle story message <id> --from <role> [--to <role>|*] --body <text>`.
func storyMessageCommand() *cobra.Command {
	var from, to, body string
	cmd := &cobra.Command{
		Use:   "message <id>",
		Short: "Append a directed agent message to a story",
		Long: `Write a directed agent message onto a story's ledger (kind agent_message).

--from is the sending role (orchestrator, executor, human, …). --to defaults
to * (every next dispatch). The next gate or executor payload carries matching
messages in messages[].`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"id": args[0], "from": from, "body": body}
			if to != "" {
				req["to"] = to
			}
			return dispatch(cmd, "story-message", req)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "sending role (required)")
	cmd.Flags().StringVar(&to, "to", "", "receiving role or * (default *)")
	cmd.Flags().StringVar(&body, "body", "", "message body (required)")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("body")
	return cmd
}

// storyMessagesCommand is `satelle story messages <id> [--since RFC3339] [--to role]`.
func storyMessagesCommand() *cobra.Command {
	var since, to string
	cmd := &cobra.Command{
		Use:   "messages <id>",
		Short: "List directed agent messages on a story (read-only)",
		Long: `List agent_message ledger rows for a story, oldest first.

Read-only — a dispatched agent may call this under Bash(satelle:*). --to keeps
that role plus *. --since is RFC3339. No engagement window (the payload already
applied that).`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"id": args[0]}
			if since != "" {
				req["since"] = since
			}
			if to != "" {
				req["to"] = to
			}
			return dispatch(cmd, "story-messages", req)
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "RFC3339 lower bound on created_at")
	cmd.Flags().StringVar(&to, "to", "", "receiving role (plus *)")
	return cmd
}

// Help for the shared story/task/execution surface. One factory builds the three
// groups, so their help is written once and PARAMETERISED — the constraints that
// genuinely differ (seat arbitration and the definition freeze are story-only,
// --parent is execution-only) are named per group rather than repeated three
// times with two of them wrong (sty_a499e7f5).

func groupParentLong(group string) string {
	switch group {
	case "task":
		return `A task is an authored HEADER — a re-runnable definition of work and how its
success is verified — not a running lifecycle. The markdown file under
.satelle/tasks is the source of truth; the database only indexes it.

Each RUN of a task is an execution: satelle execution create --parent <tsk_id>.
Retire a superseded header with archive, which is record disposition, not a
workflow status.`
	case "execution":
		return `An execution is one isolated RUN of a task. Create it with --parent <tsk_id>:
without that parent it has no definition to run and no validators to gate it.

The run carries the lifecycle (its before/after validators gate entry and
close) while the task header it points at stays a stable definition. Record the
run output with satelle execution record <exe_id>.`
	}
	return `A story is the unit of work satelle governs: it carries the goal, the
acceptance criteria, and the route it walks to done. Edits to the repo require
one engaged in a performing state — that is what the edit gate enforces.

Reach for create/set to drive the lifecycle, get/list to read, attach/doc/docs
to carry evidence, and route to see the steps and every verdict so far. The
category chosen at create selects the route; see satelle help create-story.`
}

func groupCreateLong(group string) string {
	switch group {
	case "task":
		return `Create a task header — the definition, not a run. Use it when the work is
repeatable and you want one place that says what to do and how success is
checked; use story create for a one-off slice with acceptance criteria.

--title is required. The header is written as authored markdown under
.satelle/tasks and indexed from there, so the file remains the source of truth.`
	case "execution":
		return `Create an execution: one isolated run of an existing task.

--parent <tsk_id> is what makes it a run rather than an orphan, and the task
header it names supplies both the action and the validators that gate the run.
Record what the run produced with satelle execution record.`
	}
	return `Create a story. Reach for it before touching the repo: an edit needs an
engaged story, and engagement starts here.

--category is not cosmetic — it selects the ROUTE the story will walk, and it
freezes once the story leaves its entry state, so a wrong category then costs a
gated story amend at best. When [review] gate_create is on, the create-review
gate judges the draft and can REJECT it: the story is then not created, and the
notes say what to fix.`
}

func groupGetLong(group string) string {
	return `Print one ` + group + ` by id as JSON — every field, including tags and the
acceptance criteria. Reach for it when you need the full record; list gives you
the rows, this gives you the item.

Read-only: no gate runs and nothing is recorded.`
}

func groupListLong(group, plural string) string {
	return `List ` + plural + ` as JSON, newest first, capped at 500 rows unless --limit says
otherwise. Reach for it to find an id or survey state; use get for one item.

--tag matches a tag EXACTLY, and in a multi-value namespace it is an ANY-match:
--tag sprint:4 returns an item carrying sprint:4 among others.`
}

func groupSetLong(group string) string {
	if group == "story" {
		return `Update a story: only the flags you pass change, so a status move never
rewrites the body by accident.

--status is a workflow transition, not an assignment. Its gates run and can
REFUSE it, and a status that skips a step is refused before any gate. Called by
an agent, a gate that outlasts the call returns a handle and delivers its verdict
by notification: never poll (satelle help agent-dispatch). Engaging statuses
also take the engagement seat, which is arbitrated per project.
title/body/acceptance/category are FROZEN once the story leaves its entry
state; status, tags, priority, estimate and actual stay mutable. --tags
replaces the whole set, while --add-tags/--remove-tags are additive and may not
be combined with it.`
	}
	return `Update a ` + group + `: only the flags you pass change, so a status move never
rewrites the body by accident.

--status is a workflow transition whose gates run and can REFUSE it; a status
that skips a step is refused before any gate. --tags replaces the whole tag set,
while --add-tags/--remove-tags are additive and may not be combined with it.`
}
