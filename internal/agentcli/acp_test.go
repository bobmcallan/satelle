package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
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

// writeFakeACPPeer writes an executable Python peer that speaks a minimal ACP session.
// extra is injected at the start of session/prompt (before the final answer chunk).
// Optional final is the text of the trailing agent_message_chunk; omit for the
// default decision JSON, pass "" to emit no trailing chunk (extra alone is the
// full stream — sty_844b6ab1 capture tests use this instead of a second peer).
func writeFakeACPPeer(t *testing.T, extra string, final ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-acp-peer")
	finalText := `{"decision":"accept","notes":"ok"}`
	if len(final) > 0 {
		finalText = final[0]
	}
	finalChunk := ""
	if finalText != "" {
		// JSON-escape finalText for embedding in the Python string literal.
		esc, err := json.Marshal(finalText)
		if err != nil {
			t.Fatal(err)
		}
		finalChunk = `
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":` + string(esc) + `}}}})
`
	}
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
` + extra + finalChunk + `
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

// TestACPRunner_EffortInjection (sty_657f77b9 / sty_aa726901): Grok-shaped ACP
// spawn gets --reasoning-effort on argv before stdio when Effort is set.
// set_config_option is also accepted for reasoning_effort/effort.
func TestACPRunner_EffortInjection(t *testing.T) {
	peer := writeFakeACPPeer(t, "")
	// Wrapper must be Grok-shaped (sty_aa726901 allowlist) so argv injection runs.
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "argv.log")
	wrap := filepath.Join(dir, "grok-agent-wrapper")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvLog + "\nexec " + peer + " \"$@\"\n"
	if err := os.WriteFile(wrap, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := RunnerFromBinding(InterfaceACP, wrap+" stdio")
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(context.Background(), Request{
		SystemPrompt: "rubric",
		Payload:      `{}`,
		Effort:       "high",
		Model:        "grok-4.5",
	})
	if err != nil {
		t.Fatalf("Run with effort: %v", err)
	}
	if !strings.Contains(string(out), `"decision":"accept"`) {
		t.Fatalf("stdout = %q", out)
	}
	b, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("argv log: %v", err)
	}
	argv := string(b)
	if !strings.Contains(argv, "--reasoning-effort") || !strings.Contains(argv, "high") {
		t.Fatalf("spawn argv missing --reasoning-effort high:\n%s", argv)
	}
	// Before trailing stdio.
	if !strings.Contains(argv, "--reasoning-effort\nhigh\nstdio") &&
		!strings.Contains(argv, "--reasoning-effort high") {
		// printf '%s\n' puts each arg on its own line
		lines := strings.Split(strings.TrimSpace(argv), "\n")
		found := false
		for i := 0; i+1 < len(lines); i++ {
			if lines[i] == "--reasoning-effort" && lines[i+1] == "high" {
				// must appear before a stdio token later
				for j := i + 2; j < len(lines); j++ {
					if lines[j] == "stdio" {
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatalf("expected --reasoning-effort high before stdio in argv lines: %q", lines)
		}
	}
	if strings.Contains(r.Command(), "reasoning-effort") {
		t.Error("Command() evidence should not bake runtime effort")
	}
}

func TestACPRunner_RejectsEffortPlaceholder(t *testing.T) {
	if _, err := RunnerFromBinding(InterfaceACP, "grok agent {effort} stdio"); err == nil {
		t.Fatal("expected reject of {effort} placeholder on acp command")
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

// sty_844b6ab1 — ACP capture segments on tool_call fences and keeps the last
// agent_message run (CaptureAnswer default). Tests extend writeFakeACPPeer:
// extra = pre-final updates; optional final overrides/suppresses the trailing
// answer chunk ("" = extra alone is the whole stream).

// TestACPCapture_DropsNarrationFencedByToolCall (AC3): narration chunks, then
// tool_call / tool_call_update, then the default decision answer.
// CaptureAnswer (default) must return only the answer.
func TestACPCapture_DropsNarrationFencedByToolCall(t *testing.T) {
	extra := `
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"I'll reconstruct the story context"}}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":" and then summarise."}}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"tool_call","toolCallId":"c1","title":"list_dir","kind":"read"}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"completed"}}})
`
	peer := writeFakeACPPeer(t, extra)
	r, err := RunnerFromBinding(InterfaceACP, peer+" stdio")
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
	got := string(out)
	if strings.Contains(got, "I'll reconstruct") || strings.Contains(got, "then summarise") {
		t.Fatalf("narration leaked into captured output: %q", got)
	}
	if !strings.Contains(got, `"decision":"accept"`) {
		t.Fatalf("answer missing from captured output: %q", got)
	}
	if strings.Contains(got, "summarise.{\"decision\"") || strings.Contains(got, "summarise.{") {
		t.Fatalf("narration was concatenated onto answer: %q", got)
	}
}

// TestACPCapture_SingleUnfencedRun (AC5): multi-chunk answer with no tool fence
// must be returned in full — never last-chunk truncation.
func TestACPCapture_SingleUnfencedRun(t *testing.T) {
	extra := `
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Moved from "}}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"backlog to plan "}}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"after intent review."}}}})
`
	peer := writeFakeACPPeer(t, extra, "") // no trailing decision — extra is the full stream
	r, err := RunnerFromBinding(InterfaceACP, peer+" stdio")
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(context.Background(), Request{SystemPrompt: "x", Payload: "{}"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "Moved from backlog to plan after intent review."
	if string(out) != want {
		t.Fatalf("unfenced multi-chunk capture = %q, want %q", out, want)
	}
}

// TestACPCapture_FullKeepsVerdictBeforeTrailingChatter (AC6 half): with
// CaptureFull, decision JSON before a tool fence + trailing "Done." all stay
// in the blob so parseDecision can still find the verdict.
func TestACPCapture_FullKeepsVerdictBeforeTrailingChatter(t *testing.T) {
	extra := `
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"{\"decision\":\"accept\",\"notes\":\"ok\"}"}}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"tool_call","toolCallId":"c1","title":"noop","kind":"other"}}})
`
	peer := writeFakeACPPeer(t, extra, "Done.")
	r, err := RunnerFromBinding(InterfaceACP, peer+" stdio")
	if err != nil {
		t.Fatal(err)
	}
	full, err := r.Run(context.Background(), Request{
		SystemPrompt: "x", Payload: "{}", Capture: CaptureFull,
	})
	if err != nil {
		t.Fatalf("CaptureFull Run: %v", err)
	}
	if !strings.Contains(string(full), `"decision":"accept"`) {
		t.Fatalf("CaptureFull dropped decision: %q", full)
	}
	if !strings.Contains(string(full), "Done.") {
		t.Fatalf("CaptureFull dropped trailing chatter: %q", full)
	}
	answer, err := r.Run(context.Background(), Request{
		SystemPrompt: "x", Payload: "{}", Capture: CaptureAnswer,
	})
	if err != nil {
		t.Fatalf("CaptureAnswer Run: %v", err)
	}
	if strings.Contains(string(answer), `"decision":"accept"`) {
		t.Fatalf("CaptureAnswer unexpectedly kept earlier decision segment: %q", answer)
	}
	if string(answer) != "Done." {
		t.Fatalf("CaptureAnswer = %q, want Done.", answer)
	}
}

// TestACPCapture_ExcludesThoughtChunks (AC7): thought text must not appear in
// the captured output, and must not segment an answer run.
func TestACPCapture_ExcludesThoughtChunks(t *testing.T) {
	extra := `
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"SECRET_REASONING_BEFORE"}}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Moved from "}}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"SECRET_REASONING_MID"}}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"backlog to plan."}}}})
`
	peer := writeFakeACPPeer(t, extra, "")
	r, err := RunnerFromBinding(InterfaceACP, peer+" stdio")
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(context.Background(), Request{SystemPrompt: "x", Payload: "{}"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "SECRET_REASONING") {
		t.Fatalf("agent_thought_chunk leaked into capture: %q", got)
	}
	if got != "Moved from backlog to plan." {
		t.Fatalf("thought segmented or truncated answer: %q", got)
	}
}

// TestACPCapture_InterleavedToolFenced (AC8 fenced limb): the solidsafe-shaped
// pattern — narration, tool fence, answer — keeps only the answer.
func TestACPCapture_InterleavedToolFenced(t *testing.T) {
	extra := `
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"I'll pull the story record so the step summary reflects what actually moved."}}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"tool_call","toolCallId":"c1","title":"read_file","kind":"read"}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"completed"}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Checking"}}}})
`
	// Post-tool "Checking" + default decision form one unfenced answer segment.
	peer := writeFakeACPPeer(t, extra)
	r, err := RunnerFromBinding(InterfaceACP, peer+" stdio")
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(context.Background(), Request{SystemPrompt: "x", Payload: "{}"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "I'll pull the story record") {
		t.Fatalf("pre-tool narration leaked: %q", got)
	}
	if !strings.Contains(got, "Checking") || !strings.Contains(got, `"decision":"accept"`) {
		t.Fatalf("post-tool answer incomplete: %q", got)
	}
}

func TestACPRunner_ModelSetConfigRejected(t *testing.T) {
	// Peer rejects model set_config_option.
	script := `#!/usr/bin/env python3
import sys, json
def send(o):
    sys.stdout.write(json.dumps(o)+"\n"); sys.stdout.flush()
while True:
    line = sys.stdin.readline()
    if not line:
        break
    msg = json.loads(line)
    mid = msg.get("id")
    method = msg.get("method")
    if method == "initialize":
        send({"jsonrpc":"2.0","id":mid,"result":{"protocolVersion":1,"agentCapabilities":{},"authMethods":[{"id":"cached_token"}]}})
    elif method == "authenticate":
        send({"jsonrpc":"2.0","id":mid,"result":{}})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":mid,"result":{"sessionId":"sess_test"}})
    elif method == "session/set_config_option":
        params = msg.get("params") or {}
        if params.get("configId") == "model":
            send({"jsonrpc":"2.0","id":mid,"error":{"code":-32000,"message":"unknown model"}})
        else:
            send({"jsonrpc":"2.0","id":mid,"result":{}})
    elif method == "session/prompt":
        send({"jsonrpc":"2.0","id":mid,"result":{"stopReason":"end_turn"}})
    elif mid is not None:
        send({"jsonrpc":"2.0","id":mid,"result":{}})
`
	dir := t.TempDir()
	path := dir + "/peer.py"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := RunnerFromBinding(InterfaceACP, path+" stdio")
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background(), Request{
		SystemPrompt: "rubric",
		Payload:      `{}`,
		Model:        "opus",
	})
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("want model set_config_option error, got %v", err)
	}
}
