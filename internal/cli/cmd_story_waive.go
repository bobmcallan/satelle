package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/waiver"
)

// waiveConfirmer obtains the developer's attestation for `story waive`. The
// default reads the controlling terminal; only a test in this package replaces
// it — there is deliberately no flag or environment variable that does, since an
// agent could set either.
var waiveConfirmer waiver.Confirmer = waiver.TTYConfirmer{}

func storyWaiveCommand() *cobra.Command {
	var gate, check, reason string
	cmd := &cobra.Command{
		Use:   "waive <id> --gate <skill> --check <name> --reason <text>",
		Short: "Waive one named check of one gate on one story — a developer decision, run from a terminal",
		Long: `Record that the developer waives ONE named check of ONE gate on ONE story, so a
gate that cannot be satisfied after the fact is closed by a decision on record.

It writes one gate_waiver ledger row (actor developer) only after you retype the
story id on the controlling terminal. An agent session is refused, and so is a
run with no controlling terminal; there is no stdin fallback.

A waiver reaches only that story's payload for that gate. Whether the gate's
rubric honours it is the rubric's decision; a verdict that does records the check
in its "waived" list. Honest posture: an agent with a pty or direct access to
the ledger store could still fake it. See satelle help agent-dispatch.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			if _, err := a.Store.Stories.Get(cmd.Context(), args[0]); err != nil {
				return fmt.Errorf("waiver: story %s: %w", args[0], err)
			}
			w, err := waiver.Record(cmd.Context(), a.Store.Ledger,
				waiver.Input{StoryID: args[0], Gate: gate, Check: check, Reason: reason},
				waiveConfirmer, time.Now())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "waiver %s recorded — %s waived check %s of gate %s on %s\n",
				w.ID, w.Actor, w.Check, w.Gate, w.StoryID)
			return nil
		},
	}
	cmd.Flags().StringVar(&gate, "gate", "", "the gate's skill (required)")
	cmd.Flags().StringVar(&check, "check", "", "the named check of that gate's rubric (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "why the check is waived (required)")
	return cmd
}
