// `satelle remote run <story-id>` hands one story to the remote that serves its
// segment: it selects from the local remote definitions those whose contract
// accepts the story's status and category, and fires the chosen one through the
// server. A repo with no remote, or a user signed out, is answered and left
// untouched.
package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/remotedef"
)

// errNoRemote is the answer when no defined remote serves the story's segment.
var errNoRemote = errors.New("No remote; either configure one, or stop")

// remoteRunMatch is a defined remote whose contract takes the story, with the
// segment that takes it.
type remoteRunMatch struct {
	name    string
	segment remotedef.Segment
}

func newRemoteRunCmd() *cobra.Command {
	var server, remoteID string
	run := &cobra.Command{
		Use:   "run <story-id> [--remote-id <id>]",
		Short: "Hand a story to the defined remote that serves its segment",
		Long: `Selects the remote definitions whose segments accept the story's status and
category. Needs satelle login. With none matching the answer is "No remote;
either configure one, or stop". Either way the story is left unchanged.

Refused before anything is sent when the story's status or the segment's exit
would hold an engagement seat on the local workflow.

--remote-id is the remote's id on the server (a definition carries none): with
it the story is fired at once; without it the matches and the exact fire
command are printed.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			storyID := args[0]
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			c, err := hostedClient(server)
			if err != nil {
				return err
			}
			if err := c.RequireCredential(ctx); err != nil {
				return fmt.Errorf("%w — run \"satelle login\" first", err)
			}

			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			item, err := a.Store.Stories.Get(ctx, storyID)
			if err != nil {
				return err
			}
			names, err := remotedef.Names(remoteDataDir())
			if err != nil {
				return err
			}
			var matches []remoteRunMatch
			for _, n := range names {
				if len(remotedef.Validate(remotedef.Dir(remoteDataDir(), n))) > 0 {
					fmt.Fprintf(out, "remote %s skipped: invalid (satelle remote validate %s)\n", n, n)
					continue
				}
				def, err := loadRemoteDef(n)
				if err != nil {
					return err
				}
				if seg, err := def.Accepts(item.Status, item.Category); err == nil {
					matches = append(matches, remoteRunMatch{n, seg})
				}
			}
			if len(matches) == 0 {
				return errNoRemote
			}

			local := localRoute{a.Store.Stories, a.Store.DocIndex}
			for _, m := range matches {
				reason, err := remotedef.SeatReason(ctx, local, storyID, m.segment, item.Status)
				if err != nil {
					return err
				}
				if reason != "" {
					return fmt.Errorf("remote %s: story %s %s", m.name, storyID, reason)
				}
				fmt.Fprintf(out, "remote %s: segment %s → %s\n", m.name, m.segment.Entry, m.segment.Exit)
			}

			if remoteID == "" {
				fmt.Fprintf(out, "to hand it off: satelle remote trigger fire <remote-id> --story %s\n", storyID)
				return nil
			}
			res, err := c.FireRemote(ctx, remoteID, storyID, "")
			if err != nil {
				return err
			}
			printFired(out, remoteID, res)
			return nil
		},
	}
	run.Flags().StringVar(&server, "server", "", "hosted server URL (default: the one you signed in to)")
	run.Flags().StringVar(&remoteID, "remote-id", "", "the remote's id on the server: fire the story at it now")
	return run
}
