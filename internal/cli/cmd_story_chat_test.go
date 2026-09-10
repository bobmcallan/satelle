package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestChatRefusesInLoopOrchestrator(t *testing.T) {
	g := agentstep.New(nil, nil, t.TempDir(), "")
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name != "orchestrator" {
			return config.AgentBinding{}, false
		}
		return config.AgentBinding{Command: "in-loop"}, true
	})
	_, err := g.OpenSession(t.Context(), "", workitem.Item{ID: "sty_x", Status: "in_progress", Kind: workitem.KindStory}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "in-loop") {
		t.Fatalf("want in-loop refusal, got %v", err)
	}
}

func TestChatRefusesMissingOrchestrator(t *testing.T) {
	g := agentstep.New(nil, nil, t.TempDir(), "")
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) { return config.AgentBinding{}, false })
	_, err := g.OpenSession(t.Context(), "", workitem.Item{ID: "sty_x", Kind: workitem.KindStory}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "orchestrator") {
		t.Fatalf("want missing-binding error, got %v", err)
	}
}

// TestChatAgentFlagRefusalNamesTheBinding (sty_a0372443 AC1): the refusals keep
// their existing message SHAPE; only the binding name varies, so `--agent
// reviewer` against an in-loop or non-live-capable [reviewer] says so plainly.
func TestChatAgentFlagRefusalNamesTheBinding(t *testing.T) {
	item := workitem.Item{ID: "sty_x", Status: "integration", Kind: workitem.KindStory}
	for _, tc := range []struct {
		name    string
		binding config.AgentBinding
		found   bool
		want    string
	}{
		{"in-loop", config.AgentBinding{Role: "reviewer", Command: "in-loop"}, true, "[reviewer] is in-loop"},
		{"command", config.AgentBinding{Role: "reviewer", Interface: "command", Command: "claude -p {system}"}, true, "[reviewer] interface=command is not live-capable"},
		{"missing", config.AgentBinding{}, false, "no [reviewer] binding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := agentstep.New(nil, nil, t.TempDir(), "")
			g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
				if name != "reviewer" || !tc.found {
					return config.AgentBinding{}, false
				}
				return tc.binding, true
			})
			_, err := g.OpenSession(t.Context(), "reviewer", item, nil, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

// TestChatFromRoleDefault (sty_a0372443 AC2): an explicit --from wins; with no
// flag a live SATELLE_SESSION means an agent is driving, and no session means a
// human at a prompt.
func TestChatFromRoleDefault(t *testing.T) {
	for _, tc := range []struct{ flag, session, want string }{
		{"", "", "human"},
		{"", "sess-1", "developer-agent"},
		{"reviewer", "sess-1", "reviewer"},
		{"  human  ", "sess-1", "human"},
	} {
		if got := chatFromRole(tc.flag, tc.session); got != tc.want {
			t.Errorf("chatFromRole(%q, %q) = %q, want %q", tc.flag, tc.session, got, tc.want)
		}
	}
}

// TestStoryChatFlagsRegistered: --agent/--from exist and default to the
// orchestrator console so the no-flag invocation is unchanged.
func TestStoryChatFlagsRegistered(t *testing.T) {
	c := storyChatCommand()
	for _, name := range []string{"agent", "from"} {
		if c.Flags().Lookup(name) == nil {
			t.Fatalf("missing --%s flag", name)
		}
	}
	if got := agentstep.ChatSessionBinding(""); got != "orchestrator" {
		t.Fatalf("default binding = %q", got)
	}
	if !strings.Contains(c.Long, "--agent") || !strings.Contains(c.Long, "--from") {
		t.Errorf("chat Long does not document the flags: %q", c.Long)
	}
}

func TestOpenerFromBindingLiveCapable(t *testing.T) {
	if _, err := agentcli.OpenerFromBinding(agentcli.InterfaceCommand, "claude -p {system}"); !errors.Is(err, agentcli.ErrNotLiveCapable) {
		t.Fatalf("command: %v", err)
	}
	if _, err := agentcli.OpenerFromBinding("", "in-loop"); !errors.Is(err, agentcli.ErrNotLiveCapable) {
		t.Fatalf("empty: %v", err)
	}
	op, err := agentcli.OpenerFromBinding(agentcli.InterfaceACP, "grok agent stdio")
	if err != nil || op == nil {
		t.Fatalf("acp opener: %v %v", op, err)
	}
	op, err = agentcli.OpenerFromBinding(agentcli.InterfaceStream, agentcli.DefaultClaudeStreamCommand)
	if err != nil || op == nil {
		t.Fatalf("stream opener: %v %v", op, err)
	}
}

func TestStoryChatCommandRegistered(t *testing.T) {
	c := storyChatCommand()
	if c.Name() != "chat" {
		t.Fatalf("name = %q", c.Name())
	}
	if !strings.Contains(c.Short, "orchestrator") && !strings.Contains(c.Long, "orchestrator") {
		t.Errorf("chat help missing orchestrator: %q / %q", c.Short, c.Long)
	}
}
