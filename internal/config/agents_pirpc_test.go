package config

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// interface = "pi-rpc" (sty_79ce4858): pi's RPC mode is a transport only a pi
// command may use.

func TestPiRPCInterfaceLoadsForAPiCommand(t *testing.T) {
	ac, err := loadAgentsText(t, "[planner]\nrole = \"agent\"\ninterface = \"pi-rpc\"\ncommand = \""+agentcli.PiRPCCommand+"\"\n")
	if err != nil {
		t.Fatalf("a pi-rpc binding with a pi command must load: %v", err)
	}
	if got := ac.Agents["planner"].ResolvedInterface(); got != InterfacePiRPC {
		t.Errorf("interface = %q, want %q", got, InterfacePiRPC)
	}
}

func TestPiRPCInterfaceRefusedForAnotherAdapter(t *testing.T) {
	_, err := loadAgentsText(t, "[planner]\nrole = \"agent\"\ninterface = \"pi-rpc\"\ncommand = \""+agentcli.DefaultClaudeCommand+"\"\n")
	if err == nil || !strings.Contains(err.Error(), "pi-rpc") || !strings.Contains(err.Error(), "claude adapter") {
		t.Fatalf("err = %v, want a refusal naming pi-rpc and the claude adapter", err)
	}
}
