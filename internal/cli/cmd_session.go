package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

func init() {
	register(newSessionCmd())
}

// newSessionCmd builds `satelle session`: the identity an agent session goes by.
func newSessionCmd() *cobra.Command {
	session := &cobra.Command{
		Use:   "session",
		Short: "The identity an agent session goes by (see satelle help session)",
		Long: `An agent session is named from what satelle knows about where it runs: the
short host, the repo, the worktree and the engaged story. ` + "`satelle session name`" + `
prints that identity; the launcher ` + "`satelle agents install claude`" + ` writes passes it
to the harness at launch. See: satelle help session.`,
	}
	var harness string
	name := &cobra.Command{
		Use:   "name",
		Short: "Print this session's name: <host> · <repo>[ · <worktree>][ · <story>]",
		Long: `Print the one identity an agent session in this tree goes by: the short host,
the repo name, the worktree directory (only for a linked worktree) and the
engaged story (only when this tree holds a live seat), joined by " · ".

The launcher written by ` + "`satelle agents install claude`" + ` passes it to the harness
as its session name. With --harness, a harness that declares no launch name flag
prints "title: unavailable" naming it instead. See: satelle help session.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if h := strings.TrimSpace(harness); h != "" && len(agentcli.LaunchNameFlags(h)) == 0 {
				fmt.Fprintf(out, "title: unavailable (%s has no launch name flag)\n", h)
				return nil
			}
			fmt.Fprintln(out, currentSessionName(initRepoRoot("")))
			return nil
		},
	}
	name.Flags().StringVar(&harness, "harness", "", "harness the name is for (claude, grok, pi, cursor); one with no launch name flag prints title: unavailable")
	session.AddCommand(name)
	return session
}

// currentSessionName is hosted.SessionName for the tree at repoRoot. The story
// segment is added only for a seat that resolveSeatsFor reports as this session's
// AND that was engaged from this tree: with one live seat resolveSeatsOn answers
// for any tree, so a launch from the main tree must not take the story a sibling
// worktree holds. A seat that cannot be read leaves the name without a story: the
// name is a label, never a reason to fail a launch.
func currentSessionName(repoRoot string) string {
	story := ""
	if info, engaged, _, err := resolveSeatsFor(false, config.ResolveSession(), repoRoot); err == nil &&
		engaged && sameTree(info.Worktree, repoRoot) {
		story = info.ItemID
	}
	return hosted.SessionName(repoRoot, story)
}
