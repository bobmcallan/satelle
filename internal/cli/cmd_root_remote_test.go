package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Remotes are defined, published, secreted, launched and run through
// satelle-server. The CLI keeps no remote control plane: neither group is a
// command, and no command anywhere in the tree is named for one.
func TestRootHasNoRemoteControlPlane(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Name() == "remote" || sub.Name() == "secret" {
				t.Errorf("%q is still a command", sub.CommandPath())
			}
			walk(sub)
		}
	}
	walk(NewRootCmd())

	for _, group := range []string{"remote", "secret"} {
		out, err := runRoot(t, group)
		if err == nil {
			t.Errorf("satelle %s must be an unknown command; it printed %q", group, out)
		} else if !strings.Contains(err.Error(), "unknown command") {
			t.Errorf("satelle %s = %v, want an unknown-command error", group, err)
		}
	}
	// The retired verbs are not reachable under any other group either.
	for _, args := range [][]string{
		{"remote", "publish"}, {"remote", "fetch"}, {"remote", "start"}, {"remote", "run"},
		{"remote", "wake"}, {"remote", "trigger"}, {"remote", "create"}, {"remote", "validate"},
		{"remote", "test"}, {"remote", "key"}, {"remote", "retire"}, {"secret", "push"}, {"secret", "list"},
	} {
		if _, err := runRoot(t, args...); err == nil {
			t.Errorf("satelle %s must fail", strings.Join(args, " "))
		}
	}
}

// `remote join` went with the rest of the group: the node mode is `node join`.
func TestRemoteJoinIsNotACommand(t *testing.T) {
	out, err := runRoot(t, "remote", "join", "--relay", "x", "--session", "s", "--token-stdin")
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("satelle remote join = %q, %v; want an unknown-command error", out, err)
	}
}

// The one remote verb left is the node mode: hidden, so no help lists it.
func TestNodeJoinIsHidden(t *testing.T) {
	for _, c := range NewRootCmd().Commands() {
		if c.Name() == "node" && !c.Hidden {
			t.Error("satelle node must be hidden: it is a node mode, not a developer command")
		}
	}
}
