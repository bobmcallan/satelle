// `satelle help` renders the embedded help topics to the terminal — the
// create-story path and the reviewer checks (sty_82c456a0). With no argument it
// lists the topics; with a topic name it prints that guide. The same topics back
// the web `/help` page (internal/help is the shared source). This is distinct
// from cobra's per-command `--help`: it documents the process, not the flags.
package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/help"
)

func init() {
	cmd := &cobra.Command{
		Use:   "help [topic]",
		Short: "Read process guides (create-story path, reviewer checks)",
		Long: `help renders satelle's process guides. Run with no argument to list the
topics, or pass a topic name to print that guide. These document the process
(how a story flows and what each reviewer checks); for command flags use
` + "`satelle <command> --help`" + `.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "Topics — `satelle help <topic>`:")
				fmt.Fprintln(tw)
				for _, t := range help.List() {
					fmt.Fprintf(tw, "  %s\t%s\n", t.Name, t.Title)
				}
				return tw.Flush()
			}
			t, ok := help.Get(args[0])
			if !ok {
				return fmt.Errorf("unknown help topic %q — run `satelle help` to list topics, or `satelle %s --help` for command help", args[0], args[0])
			}
			fmt.Fprintln(out, t.Body)
			return nil
		},
	}
	register(cmd)
}
