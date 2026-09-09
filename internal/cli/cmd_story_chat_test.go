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
	_, err := g.OpenOrchestrator(t.Context(), workitem.Item{ID: "sty_x", Status: "in_progress", Kind: workitem.KindStory}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "in-loop") {
		t.Fatalf("want in-loop refusal, got %v", err)
	}
}

func TestChatRefusesMissingOrchestrator(t *testing.T) {
	g := agentstep.New(nil, nil, t.TempDir(), "")
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) { return config.AgentBinding{}, false })
	_, err := g.OpenOrchestrator(t.Context(), workitem.Item{ID: "sty_x", Kind: workitem.KindStory}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "orchestrator") {
		t.Fatalf("want missing-binding error, got %v", err)
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
