// `satelle remote retire <remote-id>` retires a registered remote on the hosted
// server after an explicit confirmation. It is the only caller of
// hosted.Client.RetireRemote: stopping or tearing down a node never retires its
// remote (a shutdown-lifecycle remote starts a fresh node per fire).
package cli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// wakeKeyUnnamed stands in for the wake key's name when no --def says it.
const wakeKeyUnnamed = "named by your definition's wake_key"

func newRemoteRetireCmd() *cobra.Command {
	var server, def string
	var yes bool
	retire := &cobra.Command{
		Use:   "retire <remote-id>",
		Short: "Retire a registered remote on the server (soft: its fire history is kept)",
		Long: `Retires the remote on the server: it leaves every list, its triggers are
deleted so schedule and push stop, and its name is freed. Its fire history stays
readable. Only its creator or a project admin may retire it.

It asks you to type 'yes' (--yes skips that); nothing else, such as stopping a
node, ever retires a remote. The wake secret is left in place: delete it with
DELETE /api/v1/secrets/<key>; --def names the wake_key to show.

` + remoteIDHelp,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			key, route := wakeKeyUnnamed, "<key>"
			if def != "" {
				d, err := loadRemoteDef(def)
				if err != nil {
					return err
				}
				if d.WakeKey != "" {
					key, route = d.WakeKey, d.WakeKey
				}
			}
			out := cmd.OutOrStdout()
			if !yes {
				fmt.Fprintf(out, "Retire remote %s? Its triggers stop and its name is freed; fire history is kept. Type 'yes' to continue: ", id)
				line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if strings.TrimSpace(line) != "yes" {
					fmt.Fprintln(out, "retire: aborted — nothing sent")
					return nil
				}
			}
			c, err := remoteClient(server)
			if err != nil {
				return err
			}
			if err := c.RetireRemote(cmd.Context(), id); err != nil {
				return err
			}
			fmt.Fprintf(out, "remote %s retired\n", id)
			fmt.Fprintln(out, "fire history is kept (readable by its creator or a project admin)")
			fmt.Fprintf(out, "the wake secret (%s) is not removed — delete it with DELETE /api/v1/secrets/%s\n", key, route)
			return nil
		},
	}
	retire.Flags().BoolVar(&yes, "yes", false, "retire without asking for confirmation")
	retire.Flags().StringVar(&server, "server", "", "hosted server URL (default: the one you signed in to)")
	retire.Flags().StringVar(&def, "def", "", "the remote definition (by name) whose wake_key to name in the output")
	return retire
}
