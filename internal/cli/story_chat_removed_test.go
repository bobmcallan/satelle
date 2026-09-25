package cli

import (
	"strings"
	"testing"
)

// TestStoryChatIsNotACommand (sty_6f9ba7ca AC1): `satelle story chat` is gone.
// It is not registered under `story`, its help does not list it, and invoking
// it is refused rather than opening a session. `story rework` (AC2) stays.
func TestStoryChatIsNotACommand(t *testing.T) {
	root := NewRootCmd()
	story, _, err := root.Find([]string{"story"})
	if err != nil || story == nil || story.Name() != "story" {
		t.Fatalf("story command not found: %v", err)
	}
	names := map[string]bool{}
	for _, c := range story.Commands() {
		names[c.Name()] = true
	}
	if names["chat"] {
		t.Fatal("`story chat` is still registered")
	}
	if !names["rework"] {
		t.Fatal("`story rework` must stay registered")
	}

	// The command list `satelle story --help` prints is the usage template's
	// walk over these same registered commands.
	if usage := story.UsageString(); strings.Contains(usage, "chat") {
		t.Errorf("`satelle story --help` still lists chat:\n%s", usage)
	}

	// Resolve the invocation the way Execute does, without running the root's
	// pre-run (which initialises process-wide state other tests depend on): the
	// unknown word stays an argument to the `story` group, which refuses it.
	cmd, args, err := NewRootCmd().Find([]string{"story", "chat", "sty_x"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if cmd.Name() != "story" || len(args) != 2 || args[0] != "chat" {
		t.Fatalf("`story chat` resolved to %q %v, want the story group with args [chat sty_x]", cmd.Name(), args)
	}
	if err := cmd.RunE(cmd, args); err == nil || !strings.Contains(err.Error(), `unknown command "chat"`) {
		t.Fatalf("`satelle story chat sty_x` must be refused as an unknown command, got %v", err)
	}
}
