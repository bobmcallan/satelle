// Package cli wires the satelle command-line interface.
//
// Subcommands self-register via init() calling register(), so adding a new
// verb is one new file under internal/cli/ — no edits to a central switch.
// Ported from satellites' internal/cli/root.go, stripped of the engagement
// ticker (the local OSS tier has no leased session to heartbeat).
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var registered []*cobra.Command

// register attaches a subcommand to the root. Called from each subcommand
// file's init().
func register(c *cobra.Command) {
	registered = append(registered, c)
}

// NewRootCmd returns the root cobra command with every registered
// subcommand attached.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "satelle",
		Short: "Satelle — local-first authored-process substrate CLI",
		Long: `satelle governs the authored process for agent-driven work, backed by a
local per-repo database. The OSS tier runs 100% locally with no server
dependency. See https://github.com/bobmcallan/satelle for docs.`,
		Version:       versionLine(),
		SilenceUsage:  true,
		SilenceErrors: true,
		// Bootstrap seam: open the local store (config + db) only for commands
		// that carry the storeAnnotation, and close it after. `version` and
		// `--help` are unannotated, so they never create a database.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Annotations[storeAnnotation] == "1" {
				return openAppForCmd(cmd)
			}
			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			closeAppForCmd(cmd)
			return nil
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	for _, c := range registered {
		root.AddCommand(c)
	}
	return root
}

// Execute runs the root command and, on an unknown subcommand, follows the
// bare cobra error with the command's usage so a mistyped command guides the
// user instead of dead-ending. Cobra already embeds a "Did you mean this?"
// nearest-match in the error for close typos; this adds the usage block on
// top. Normal RunE errors keep their quiet, usage-free output (root has
// SilenceUsage/SilenceErrors set). Returns the command's error so the caller
// sets a non-zero exit.
func Execute() error {
	// Repo-local binary precedence (sty_fe3ee313): if this repo pins its own
	// satelle under .satelle/satelle, run THAT binary instead of the global one.
	if code, handled := reexecLocalIfPresent(); handled {
		os.Exit(code)
	}
	root := NewRootCmd()
	err := root.Execute()
	if err != nil {
		fmt.Fprintln(root.ErrOrStderr(), err)
		if isUnknownCommandErr(err) {
			printUnknownCommandHelp(root.ErrOrStderr(), root)
		}
	}
	return err
}

// isUnknownCommandErr reports whether err is cobra's "unknown command"
// error. Cobra exposes no typed error for this, but the message prefix
// (`unknown command %q for %q`) is stable across releases.
func isUnknownCommandErr(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "unknown command")
}

// printUnknownCommandHelp writes the root usage block so an unknown command
// response names the available commands.
func printUnknownCommandHelp(w io.Writer, root *cobra.Command) {
	fmt.Fprintln(w)
	fmt.Fprint(w, root.UsageString())
}
