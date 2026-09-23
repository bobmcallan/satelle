package agentstep

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// writeFakeACPPeer spawns a minimal ACP peer (same protocol shape as
// agentcli's own fake-peer test harness, acp_test.go:92) that answers
// initialize/authenticate/session/new/session/prompt with a bare, non-JSON
// -envelope decision — exactly what a real ACP-fronted agent (Codex, Grok
// over ACP) returns: no `result`/`text`/`modelUsage` wrapper at all.
func writeFakeACPPeer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-acp-peer")
	final := `{"decision":"accept","notes":"ok"}`
	esc, err := json.Marshal(final)
	if err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/env python3
import json, sys

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

def read():
    line = sys.stdin.readline()
    if not line:
        return None
    return json.loads(line)

while True:
    msg = read()
    if msg is None:
        break
    mid = msg.get("id")
    method = msg.get("method")
    if method == "initialize":
        send({"jsonrpc":"2.0","id":mid,"result":{"protocolVersion":1,"agentCapabilities":{},"authMethods":[{"id":"cached_token"}]}})
    elif method == "authenticate":
        send({"jsonrpc":"2.0","id":mid,"result":{}})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":mid,"result":{"sessionId":"sess_test"}})
    elif method == "session/set_config_option":
        send({"jsonrpc":"2.0","id":mid,"result":{}})
    elif method == "session/prompt":
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":` + string(esc) + `}}}})
        send({"jsonrpc":"2.0","id":mid,"result":{"stopReason":"end_turn"}})
    elif method == "session/cancel":
        pass
    else:
        if mid is not None:
            send({"jsonrpc":"2.0","id":mid,"result":{}})
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestACPDispatch_ModelUnavailable pins AC4: a dispatch over the ACP transport
// records the explicit ModelUnavailable marker, never the configured alias and
// never an empty value. ACP builds no UsageResult of its own (acp.go returns
// only raw bytes), so agentcli.UnwrapUsage sees a bare, non-envelope decision
// JSON and leaves ModelResolved empty; toVerbModels — the single normalization
// point every population site funnels through (sty_87b86044) — is what turns
// that empty value into the explicit marker.
func TestACPDispatch_ModelUnavailable(t *testing.T) {
	peer := writeFakeACPPeer(t)
	runner, err := agentcli.RunnerFromBinding(agentcli.InterfaceACP, peer+" stdio")
	if err != nil {
		t.Fatal(err)
	}
	g := New(&fakeRunner{}, fakeDocs{}, t.TempDir(), "")
	const alias = "grok-4.5"
	_, usage, err := g.runOnce(context.Background(), runner, agentcli.Request{
		SystemPrompt: "rubric",
		Payload:      `{"story":{"id":"sty_1"}}`,
		Model:        alias,
	}, 0, 0)
	if err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if usage.ModelResolved != "" {
		t.Fatalf("agentcli should report no resolved model for a bare ACP decision, got %q", usage.ModelResolved)
	}
	resolved, models := toVerbModels(usage)
	if resolved == "" {
		t.Fatal("toVerbModels must never normalize to empty")
	}
	if resolved != agentcli.ModelUnavailable {
		t.Errorf("resolved = %q, want %q", resolved, agentcli.ModelUnavailable)
	}
	if resolved == alias {
		t.Errorf("resolved must not equal the configured alias %q", alias)
	}
	if models != nil {
		t.Errorf("models = %+v, want nil (ACP reports no per-model usage)", models)
	}
}
