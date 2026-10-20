package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/drive"
	"github.com/bobmcallan/satelle/internal/verb"
)

// exitParked is the exit code of a drive that stopped because a person must
// decide; 0 is a terminal story and 1 a refusal or failure.
const exitParked = 3

func storyDriveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "drive <id>",
		Short: "Drive a story along its route with no one at a prompt, parking as blocked when a person must decide",
		Long: `Present each next edge through the verb 'story set --status' uses, letting every
step's headless performer run and its gates judge, until the story is terminal.
Refuses (exit 1) a route needing the driving session: an in-loop [executor] on
an executor step, or a named agent bound in-loop. A rejection is relayed to the
performer and rework runs within its budget; when a person must decide, the
story parks at blocked with the objection recorded.

Exit: 0 terminal, 3 parked, 1 refused or failed. See 'satelle help agent-dispatch'.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE:        runStoryDrive,
	}
}

func runStoryDrive(cmd *cobra.Command, args []string) error {
	id := strings.TrimSpace(args[0])
	// The drive runs reviewers and performers for as long as the route takes: an
	// agent-facing call is handed to a detached run as a whole, and inside that
	// process every gate runs in the foreground.
	if handled, herr := handOffGate(cmd, "story-drive", id); handled {
		return herr
	}
	// A remote's run reads its secrets from satelle.dev before any agent starts,
	// and only when the host handed this process a run to read for.
	stopSecrets, err := wireRunSecrets(cmd.Context(), cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	defer stopSecrets()
	eng, _, err := engineForCmd(cmd)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	res, err := drive.Story(cmd.Context(), id, drive.Options{
		Performers: eng,
		Rework: func(ctx context.Context, id string) (drive.Rework, error) {
			var relay reworkResult
			err := runRework(cmd, id, 0, "", &relay)
			return drive.Rework{
				Converged: relay.Converged, Outcome: relay.Outcome, Rounds: relay.Rounds, LastObjection: relay.LastObjection,
			}, err
		},
		Out: out,
	})
	if err != nil {
		return err
	}
	summary := driveSummary(res)
	fmt.Fprintln(out, summary)
	verb.RecordVerdict(summary)
	if res.Parked {
		return &ExitError{Code: exitParked, Err: fmt.Errorf("%s", summary), Quiet: true}
	}
	return nil
}

// driveSummary is the one line a drive ends on.
func driveSummary(res drive.Result) string {
	switch {
	case res.Parked && res.Reason != "":
		return fmt.Sprintf("%s parked at %s: %s", res.ID, res.Status, res.Reason)
	case res.Parked:
		return fmt.Sprintf("%s parked at %s", res.ID, res.Status)
	}
	return fmt.Sprintf("%s %s (terminal)", res.ID, res.Status)
}
