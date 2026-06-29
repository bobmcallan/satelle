package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// helpRoot builds a minimal command tree: the help command plus a real `story`
// command, mirroring how routing must behave at runtime.
func helpRoot(t *testing.T) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "satelle"}
	root.AddCommand(&cobra.Command{Use: "story", Short: "Manage stories", RunE: func(*cobra.Command, []string) error { return nil }})
	root.AddCommand(newHelpCmd())
	return root
}

func runHelp(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := helpRoot(t)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestHelpRouting(t *testing.T) {
	// A real process topic prints its body.
	out, err := runHelp(t, "help", "substrate")
	if err != nil || !strings.Contains(out, "Open Knowledge Format") {
		t.Errorf("help substrate should print the topic: err=%v\n%s", err, out)
	}

	// A command name routes to that command's help, not a flat error.
	out, err = runHelp(t, "help", "story")
	if err != nil || !strings.Contains(out, "Manage stories") {
		t.Errorf("help <command> should render command help: err=%v\n%s", err, out)
	}

	// A genuinely unknown arg errors with guidance.
	if _, err := runHelp(t, "help", "definitely-not-a-thing"); err == nil {
		t.Error("help <unknown> should error")
	}
}
