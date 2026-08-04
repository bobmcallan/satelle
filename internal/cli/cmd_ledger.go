package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

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

	// Shared suite evidence (sty_183a0510): record one expensive suite run as
	// SHA-keyed evidence, let sibling stories cite it, and enumerate the facts a
	// gate needs. Typed flags — `append` deliberately keeps no --payload flag.
	var rStory, rSHA, rCommand, rOutcome, rStarted, rFinished string
	recordRunCmd := &cobra.Command{
		Use:   "record-run",
		Short: "Record a verification-suite run as SHA-keyed evidence sibling stories can cite",
		Long: `Record ONE run of an expensive verification suite as SHA-keyed evidence.

Sibling stories delivered at the same commit cite the record (satelle ledger
cite-run) instead of re-running the suite; a gate enumerates the citation with
satelle ledger citation and decides in its own check block.

--sha defaults to the current git HEAD.`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"story_id": rStory, "command": rCommand, "outcome": rOutcome}
			putIf(req, "sha", rSHA)
			putIf(req, "started_at", rStarted)
			putIf(req, "finished_at", rFinished)
			return dispatch(cmd, "ledger-record-run", req)
		},
	}
	recordRunCmd.Flags().StringVar(&rStory, "story", "", "the recording story id (required)")
	recordRunCmd.Flags().StringVar(&rSHA, "sha", "", "commit the suite ran at (default: current HEAD)")
	recordRunCmd.Flags().StringVar(&rCommand, "command", "", "the suite command that ran (required)")
	recordRunCmd.Flags().StringVar(&rOutcome, "outcome", "", "green or red (required)")
	recordRunCmd.Flags().StringVar(&rStarted, "started-at", "", "run start timestamp (RFC3339)")
	recordRunCmd.Flags().StringVar(&rFinished, "finished-at", "", "run finish timestamp (RFC3339)")
	_ = recordRunCmd.MarkFlagRequired("story")
	_ = recordRunCmd.MarkFlagRequired("command")
	_ = recordRunCmd.MarkFlagRequired("outcome")

	var cStory, cRun string
	citeRunCmd := &cobra.Command{
		Use:         "cite-run",
		Short:       "Cite a recorded suite run from another story",
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cmd, "ledger-cite-run", map[string]any{"story_id": cStory, "run_id": cRun})
		},
	}
	citeRunCmd.Flags().StringVar(&cStory, "story", "", "the citing story id (required)")
	citeRunCmd.Flags().StringVar(&cRun, "run", "", "the suite_run entry id being cited (required)")
	_ = citeRunCmd.MarkFlagRequired("story")
	_ = citeRunCmd.MarkFlagRequired("run")

	citationCmd := &cobra.Command{
		Use:   "citation [story-id]",
		Short: "Enumerate a story's suite-run citation and the facts a gate needs",
		Long: `Report the facts about a story's suite-run citation — whether one exists,
whether the cited run resolves, its outcome/command/sha, the current HEAD and
dirty flag, and whether the run's sha equals HEAD.

Enumeration only: every enumerable state exits 0 and is reported as a field.
The gate's check block names the refusal reasons and decides. Non-zero exit is
reserved for genuine errors (unknown story, git unavailable). With no argument
the story id is read from a transition payload on stdin, so a functional check
can pipe {story, from, to} straight in. See satelle help reviewer-checks.`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return dispatch(cmd, "ledger-citation", map[string]any{"id": args[0]})
			}
			in, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return err
			}
			body := map[string]any{}
			if len(bytes.TrimSpace(in)) > 0 {
				if err := json.Unmarshal(in, &body); err != nil {
					return fmt.Errorf("ledger citation: stdin JSON: %w", err)
				}
			}
			return dispatch(cmd, "ledger-citation", body)
		},
	}

	ledgerCmd.AddCommand(appendCmd, listCmd, recordRunCmd, citeRunCmd, citationCmd)
	register(ledgerCmd)
}
