package agentcli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunnerFromBinding_CommandDefault(t *testing.T) {
	r, err := RunnerFromBinding("", DefaultGrokCommand)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil || r.Name() != "grok" {
		t.Fatalf("command path runner = %v", r)
	}
	// Same as RunnerFromCommand.
	r2, err := RunnerFromCommand(DefaultGrokCommand)
	if err != nil || r2.Command() != r.Command() {
		t.Fatalf("command path divergence: %v %q vs %q", err, r.Command(), r2.Command())
	}
}

func TestRunnerFromBinding_ACPRejectsPlaceholders(t *testing.T) {
	if _, err := RunnerFromBinding(InterfaceACP, "grok agent stdio {system}"); err == nil {
		t.Fatal("expected error for {system} in acp command")
	}
	if _, err := RunnerFromBinding(InterfaceACP, "grok"); err == nil {
		t.Fatal("expected error for bare token")
	}
	r, err := RunnerFromBinding(InterfaceACP, "grok agent stdio")
	if err != nil {
		t.Fatal(err)
	}
	if r.Command() != "grok agent stdio" {
		t.Errorf("Command() = %q", r.Command())
	}
}

func TestToolsAllowMutators(t *testing.T) {
	if toolsAllowMutators("read_file,grep,list_dir") {
		t.Error("read-only should not allow mutators")
	}
	if toolsAllowMutators("Read,Grep,Bash(satelle:*)") {
		t.Error("Bash(satelle:*) alone should not allow mutators")
	}
	if !toolsAllowMutators("read_file,search_replace") {
		t.Error("search_replace should allow mutators")
	}
	if !toolsAllowMutators("Bash") {
		t.Error("unrestricted Bash should allow mutators")
	}
}

func TestIsMutatorToolKind(t *testing.T) {
	if !isMutatorToolKind("edit") || !isMutatorToolKind("execute") {
		t.Error("edit/execute should be mutators")
	}
	if isMutatorToolKind("read") || isMutatorToolKind("search") {
		t.Error("read/search should not be mutators")
	}
}

// writeFakeACPPeer writes an executable Python peer that speaks a minimal ACP session
// and emits a decision JSON as agent_message_chunk text.
func writeFakeACPPeer(t *testing.T, extra string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-acp-peer")
	// Python peer: initialize, authenticate, session/new, session/prompt + optional permission.
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
` + extra + `
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"{\"decision\":\"accept\",\"notes\":\"ok\"}"}}}})
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

func TestACPRunner_FakePeerDecision(t *testing.T) {
	peer := writeFakeACPPeer(t, "")
	// Multi-token spawn: executable peer + dummy arg (peer ignores argv).
	r, err := RunnerFromBinding(InterfaceACP, peer+" stdio")
	if err != nil {
		t.Fatal(err)
	}
	var sink bytes.Buffer
	out, err := r.Run(context.Background(), Request{
		SystemPrompt: "rubric",
		Payload:      `{"story":{"id":"sty_1"}}`,
		AllowedTools: "read_file,grep,list_dir",
		Model:        "grok-4.5",
		Sink:         &sink,
	})
	if err != nil {
		t.Fatalf("Run: %v\nsink:\n%s", err, sink.String())
	}
	if !strings.Contains(string(out), `"decision":"accept"`) {
		t.Fatalf("stdout = %q, want decision accept", out)
	}
	if sink.Len() == 0 {
		t.Error("expected Sink to receive progressive ACP lines")
	}
}

func TestACPRunner_PermissionDenyMutator(t *testing.T) {
	// Peer requests permission for edit; client must reject when tools read-only.
	extra := `
        # request permission for an edit tool before the decision chunk
        send({"jsonrpc":"2.0","id":99,"method":"session/request_permission","params":{"sessionId":"sess_test","toolCall":{"toolCallId":"c1","kind":"edit","title":"Write"},"options":[{"optionId":"allow-once","name":"Allow","kind":"allow_once"},{"optionId":"reject-once","name":"Reject","kind":"reject_once"}]}})
        # wait for client response
        resp = read()
        if resp is None or resp.get("result",{}).get("outcome",{}).get("optionId") != "reject-once":
            send({"jsonrpc":"2.0","id":mid,"error":{"code":1,"message":"expected reject-once, got %s" % (resp,)}})
            continue
`
	peer := writeFakeACPPeer(t, extra)
	r, err := RunnerFromBinding(InterfaceACP, peer+" stdio")
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(context.Background(), Request{
		SystemPrompt: "r",
		Payload:      "{}",
		AllowedTools: "read_file,grep,list_dir",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(string(out), "accept") {
		t.Fatalf("out = %q", out)
	}
}

func TestACPRunner_TimeoutKills(t *testing.T) {
	// Peer blocks forever on prompt — context cancel must return.
	dir := t.TempDir()
	path := filepath.Join(dir, "hang-peer")
	script := `#!/usr/bin/env python3
import json, sys, time
def send(obj):
    sys.stdout.write(json.dumps(obj)+"\n"); sys.stdout.flush()
def read():
    line=sys.stdin.readline()
    return json.loads(line) if line else None
while True:
    msg=read()
    if not msg: break
    mid, method = msg.get("id"), msg.get("method")
    if method=="initialize":
        send({"jsonrpc":"2.0","id":mid,"result":{"protocolVersion":1,"authMethods":[]}})
    elif method=="session/new":
        send({"jsonrpc":"2.0","id":mid,"result":{"sessionId":"s"}})
    elif method=="session/prompt":
        time.sleep(60)
    elif mid is not None:
        send({"jsonrpc":"2.0","id":mid,"result":{}})
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := RunnerFromBinding(InterfaceACP, path+" stdio")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err = r.Run(ctx, Request{SystemPrompt: "x", Payload: "{}"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
