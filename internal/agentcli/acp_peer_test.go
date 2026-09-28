package agentcli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Non-Grok ACP peer fixtures (sty_aa726901 AC1/AC4). Generic ACP coverage from
// sty_3b4909bb lives in acp_test.go; these pin the spawn behaviour for a peer
// satelle has no name for: no Grok-only --reasoning-effort argv, session effort
// config, response capture, denied mutation, Satelle-only grant contract.

// peerSpawnArg stands in for the adapter package token a real peer spawn would
// carry. It is deliberately not Grok-shaped.
const peerSpawnArg = "--peer-adapter"

// TestACPPeer_NoReasoningEffortArgv (AC1): a non-Grok spawn must NOT receive
// --reasoning-effort on argv when Effort is set; effort rides set_config_option
// (session path).
func TestACPPeer_NoReasoningEffortArgv(t *testing.T) {
	for _, effort := range []string{"low", "high"} {
		t.Run(effort, func(t *testing.T) {
			dir := t.TempDir()
			argvLog := filepath.Join(dir, "argv.log")
			cfgLog := filepath.Join(dir, "cfg.log")
			peer := writeACPPeer(t, cfgLog)
			// Wrapper name must NOT be Grok-shaped.
			wrap := filepath.Join(dir, "peer-wrapper")
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvLog + "\nexec " + peer + " \"$@\"\n"
			if err := os.WriteFile(wrap, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			// Multi-token spawn: wrapper + adapter token (no stdio subcommand); the
			// peer ignores argv content.
			cmdLine := wrap + " " + peerSpawnArg
			r, err := RunnerFromBinding(InterfaceACP, cmdLine)
			if err != nil {
				t.Fatal(err)
			}
			out, err := r.Run(context.Background(), Request{
				SystemPrompt: "rubric",
				Payload:      `{}`,
				Effort:       effort,
				Model:        "gpt-5.6-terra",
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !strings.Contains(string(out), `"decision":"accept"`) {
				t.Fatalf("stdout = %q", out)
			}
			b, err := os.ReadFile(argvLog)
			if err != nil {
				t.Fatalf("argv log: %v", err)
			}
			argv := string(b)
			if strings.Contains(argv, "--reasoning-effort") {
				t.Fatalf("non-Grok ACP spawn must not receive --reasoning-effort argv:\n%s", argv)
			}
			cfg, err := os.ReadFile(cfgLog)
			if err != nil {
				t.Fatalf("cfg log: %v", err)
			}
			cfgS := string(cfg)
			if !strings.Contains(cfgS, "reasoning_effort") || !strings.Contains(cfgS, effort) {
				t.Fatalf("want set_config_option reasoning_effort=%s in cfg log:\n%s", effort, cfgS)
			}
			if strings.Contains(r.Command(), "reasoning-effort") {
				t.Error("Command() evidence must not bake runtime effort")
			}
		})
	}
}

// TestACPPeer_InitModelEffortAndCapture covers initialization, model config,
// and CaptureAnswer vs CaptureFull on a non-Grok spawn with tool fencing (AC4).
func TestACPPeer_InitModelEffortAndCapture(t *testing.T) {
	dir := t.TempDir()
	cfgLog := filepath.Join(dir, "cfg.log")
	// Narration, tool fence, then final decision — same contract as generic ACP capture.
	extra := `
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Peer narration before tools"}}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"tool_call","toolCallId":"c1","title":"list_dir","kind":"read"}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"completed"}}})
`
	// Peer that also logs set_config_option (model/effort) like writeACPPeer.
	peer := writeACPPeerWithExtra(t, cfgLog, extra)
	wrap := filepath.Join(dir, "peer-wrapper")
	script := "#!/bin/sh\nexec " + peer + " \"$@\"\n"
	if err := os.WriteFile(wrap, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cmdLine := wrap + " " + peerSpawnArg
	r, err := RunnerFromBinding(InterfaceACP, cmdLine)
	if err != nil {
		t.Fatal(err)
	}
	var sink bytes.Buffer
	// CaptureAnswer (default): only final decision, no narration.
	out, err := r.Run(context.Background(), Request{
		SystemPrompt: "rubric",
		Payload:      `{"story":{"id":"sty_x"}}`,
		AllowedTools: "read_file,grep,list_dir",
		Model:        "gpt-5.6-terra",
		Effort:       "high",
		Sink:         &sink,
		Capture:      CaptureAnswer,
	})
	if err != nil {
		t.Fatalf("CaptureAnswer Run: %v\nsink:\n%s", err, sink.String())
	}
	got := string(out)
	if strings.Contains(got, "Peer narration") {
		t.Fatalf("CaptureAnswer leaked narration: %q", got)
	}
	if !strings.Contains(got, `"decision":"accept"`) {
		t.Fatalf("CaptureAnswer missing decision: %q", got)
	}
	cfg, err := os.ReadFile(cfgLog)
	if err != nil {
		t.Fatal(err)
	}
	cfgS := string(cfg)
	if !strings.Contains(cfgS, "initialize") {
		t.Fatalf("want initialize handshake logged:\n%s", cfgS)
	}
	if !strings.Contains(cfgS, "model") || !strings.Contains(cfgS, "gpt-5.6-terra") {
		t.Fatalf("want model set_config_option:\n%s", cfgS)
	}
	if !strings.Contains(cfgS, "reasoning_effort") || !strings.Contains(cfgS, "high") {
		t.Fatalf("want effort set_config_option:\n%s", cfgS)
	}
	if sink.Len() == 0 {
		t.Error("expected progressive Sink output")
	}

	// CaptureFull: narration + decision both present.
	r2, err := RunnerFromBinding(InterfaceACP, cmdLine)
	if err != nil {
		t.Fatal(err)
	}
	full, err := r2.Run(context.Background(), Request{
		SystemPrompt: "rubric",
		Payload:      `{}`,
		Model:        "gpt-5.6-terra",
		Effort:       "high",
		Capture:      CaptureFull,
	})
	if err != nil {
		t.Fatalf("CaptureFull Run: %v", err)
	}
	fullS := string(full)
	if !strings.Contains(fullS, "Peer narration") {
		t.Fatalf("CaptureFull dropped narration: %q", fullS)
	}
	if !strings.Contains(fullS, `"decision":"accept"`) {
		t.Fatalf("CaptureFull dropped decision: %q", fullS)
	}
}

// TestACPPeer_DenyMutation: edit permission denied under read-only tools (AC4).
func TestACPPeer_DenyMutation(t *testing.T) {
	extra := `
        send({"jsonrpc":"2.0","id":99,"method":"session/request_permission","params":{"sessionId":"sess_test","toolCall":{"toolCallId":"c1","kind":"edit","title":"Write"},"options":[{"optionId":"allow-once","name":"Allow","kind":"allow_once"},{"optionId":"reject-once","name":"Reject","kind":"reject_once"}]}})
        resp = read()
        if resp is None or resp.get("result",{}).get("outcome",{}).get("optionId") != "reject-once":
            send({"jsonrpc":"2.0","id":mid,"error":{"code":1,"message":"expected reject-once, got %s" % (resp,)}})
            continue
`
	peer := writeFakeACPPeer(t, extra)
	dir := t.TempDir()
	wrap := filepath.Join(dir, "peer-wrapper")
	if err := os.WriteFile(wrap, []byte("#!/bin/sh\nexec "+peer+" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := RunnerFromBinding(InterfaceACP, wrap+" "+peerSpawnArg)
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

// TestACPPeer_SatelleOnlyOps: kind=read allowed under Bash(satelle:*) grant;
// kind=execute still denied (current contract — pin, do not widen) (AC4 / Risk 4).
func TestACPPeer_SatelleOnlyOps(t *testing.T) {
	// First: read allowed.
	extraRead := `
        send({"jsonrpc":"2.0","id":99,"method":"session/request_permission","params":{"sessionId":"sess_test","toolCall":{"toolCallId":"c1","kind":"read","title":"Read"},"options":[{"optionId":"allow-once","name":"Allow","kind":"allow_once"},{"optionId":"reject-once","name":"Reject","kind":"reject_once"}]}})
        resp = read()
        if resp is None or resp.get("result",{}).get("outcome",{}).get("optionId") != "allow-once":
            send({"jsonrpc":"2.0","id":mid,"error":{"code":1,"message":"expected allow-once for read, got %s" % (resp,)}})
            continue
`
	peer := writeFakeACPPeer(t, extraRead)
	dir := t.TempDir()
	wrap := filepath.Join(dir, "peer-wrapper")
	if err := os.WriteFile(wrap, []byte("#!/bin/sh\nexec "+peer+" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := RunnerFromBinding(InterfaceACP, wrap+" "+peerSpawnArg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(context.Background(), Request{
		SystemPrompt: "r",
		Payload:      "{}",
		AllowedTools: "read_file,grep,list_dir,Bash(satelle:*)",
	})
	if err != nil {
		t.Fatalf("read allow Run: %v", err)
	}
	if !strings.Contains(string(out), "accept") {
		t.Fatalf("out = %q", out)
	}

	// execute still denied with Bash(satelle:*) (toolsAllowMutators treats it as non-mutator).
	extraExec := `
        send({"jsonrpc":"2.0","id":99,"method":"session/request_permission","params":{"sessionId":"sess_test","toolCall":{"toolCallId":"c1","kind":"execute","title":"Bash"},"options":[{"optionId":"allow-once","name":"Allow","kind":"allow_once"},{"optionId":"reject-once","name":"Reject","kind":"reject_once"}]}})
        resp = read()
        if resp is None or resp.get("result",{}).get("outcome",{}).get("optionId") != "reject-once":
            send({"jsonrpc":"2.0","id":mid,"error":{"code":1,"message":"expected reject-once for execute, got %s" % (resp,)}})
            continue
`
	peer2 := writeFakeACPPeer(t, extraExec)
	wrap2 := filepath.Join(dir, "peer-wrapper2")
	if err := os.WriteFile(wrap2, []byte("#!/bin/sh\nexec "+peer2+" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r2, err := RunnerFromBinding(InterfaceACP, wrap2+" "+peerSpawnArg)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := r2.Run(context.Background(), Request{
		SystemPrompt: "r",
		Payload:      "{}",
		AllowedTools: "read_file,grep,list_dir,Bash(satelle:*)",
	})
	if err != nil {
		t.Fatalf("execute deny Run: %v", err)
	}
	if !strings.Contains(string(out2), "accept") {
		t.Fatalf("out2 = %q", out2)
	}
}

// TestACPPeer_SkipsCLIOwnedAuthMethods (sty_71491143): a peer that advertises
// api-key / web-login methods must not have authenticate called for them —
// agent CLIs own their login, same posture as Claude/Grok outside Grok session
// reuse.
func TestACPPeer_SkipsCLIOwnedAuthMethods(t *testing.T) {
	dir := t.TempDir()
	authLog := filepath.Join(dir, "auth.log")
	path := filepath.Join(dir, "auth-peer")
	authEsc := strings.ReplaceAll(authLog, `\`, `\\`)
	authEsc = strings.ReplaceAll(authEsc, `"`, `\"`)
	script := `#!/usr/bin/env python3
import json, sys
AUTH = "` + authEsc + `"

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
        send({"jsonrpc":"2.0","id":mid,"result":{"protocolVersion":1,"agentCapabilities":{},"authMethods":[{"id":"api-key"},{"id":"web-login"}]}})
    elif method == "authenticate":
        with open(AUTH, "a") as f:
            f.write("authenticate methodId=%s\n" % ((msg.get("params") or {}).get("methodId"),))
        send({"jsonrpc":"2.0","id":mid,"result":{}})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":mid,"result":{"sessionId":"sess_test"}})
    elif method == "session/prompt":
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"{\"decision\":\"accept\",\"notes\":\"\"}"}}}})
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
	wrap := filepath.Join(dir, "peer-wrapper")
	if err := os.WriteFile(wrap, []byte("#!/bin/sh\nexec "+path+" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := RunnerFromBinding(InterfaceACP, wrap+" "+peerSpawnArg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(context.Background(), Request{
		SystemPrompt: "rubric",
		Payload:      `{}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(string(out), `"decision":"accept"`) {
		t.Fatalf("stdout = %q", out)
	}
	if b, err := os.ReadFile(authLog); err == nil && len(b) > 0 {
		t.Fatalf("a peer's CLI-owned auth methods must not be authenticated; got:\n%s", b)
	}
}

// writeACPPeer is like writeFakeACPPeer but logs set_config_option calls to
// cfgLog for effort/model assertions.
func writeACPPeer(t *testing.T, cfgLog string) string {
	return writeACPPeerWithExtra(t, cfgLog, "")
}

// writeACPPeerWithExtra injects Python statements into the session/prompt
// handler before the final decision chunk (for tool-fenced capture tests).
// Logs initialize + set_config_option to cfgLog for AC4 init assertions.
func writeACPPeerWithExtra(t *testing.T, cfgLog, extra string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "acp-peer")
	cfgEsc := strings.ReplaceAll(cfgLog, `\`, `\\`)
	cfgEsc = strings.ReplaceAll(cfgEsc, `"`, `\"`)
	script := `#!/usr/bin/env python3
import json, sys
CFG = "` + cfgEsc + `"

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
        params = msg.get("params") or {}
        with open(CFG, "a") as f:
            f.write("initialize protocolVersion=%s clientInfo=%s\n" % (
                params.get("protocolVersion"), params.get("clientInfo")))
        send({"jsonrpc":"2.0","id":mid,"result":{"protocolVersion":1,"agentCapabilities":{},"authMethods":[{"id":"cached_token"}]}})
    elif method == "authenticate":
        send({"jsonrpc":"2.0","id":mid,"result":{}})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":mid,"result":{"sessionId":"sess_test"}})
    elif method == "session/set_config_option":
        params = msg.get("params") or {}
        with open(CFG, "a") as f:
            f.write("%s=%s\n" % (params.get("configId"), params.get("value")))
        send({"jsonrpc":"2.0","id":mid,"result":{}})
    elif method == "session/prompt":
` + extra + `
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"{\"decision\":\"accept\",\"notes\":\"\"}"}}}})
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
