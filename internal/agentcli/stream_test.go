package agentcli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func skipWithoutPython3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
}

// writeFakeStreamPeer writes a stream-json child that records the first user
// message, answers can_use_tool control requests, and emits a result per turn.
func writeFakeStreamPeer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-stream-peer")
	script := `#!/usr/bin/env python3
import json, os, sys

mark = os.environ.get("STREAM_MARK")
if mark:
    with open(mark, "a") as f:
        f.write("start\n")

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

init_model = os.environ.get("STREAM_INIT_MODEL")
if init_model:
    send({"type":"system","subtype":"init","model":init_model,"session_id":"fake-sess"})

def read():
    line = sys.stdin.readline()
    if not line:
        return None
    return json.loads(line)

n = 0
while True:
    msg = read()
    if msg is None:
        break
    if msg.get("type") != "user":
        continue
    n += 1
    text = ""
    for c in ((msg.get("message") or {}).get("content") or []):
        if c.get("type") == "text":
            text += c.get("text") or ""
    if n == 1:
        uout = os.environ.get("USER_OUT")
        if uout:
            open(uout, "w").write(text)
    if os.environ.get("STREAM_TOOL") == "1" and n == 1:
        send({"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Read","input":{"file_path":"x"}}]}})
        send({"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"ok"}]}})
        send({"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu2","name":"Bash","input":{"command":"false"}}]}})
        send({"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu2","is_error":True,"content":"boom"}]}})
    if os.environ.get("STREAM_PERM") == "1" and n == 1:
        send({"type":"control_request","request_id":"req1","request":{"subtype":"can_use_tool","tool_name":"Edit"}})
        resp = read()
        behavior = ((((resp or {}).get("response") or {}).get("response") or {}).get("behavior")) or ""
        pout = os.environ.get("PERM_OUT")
        if pout:
            open(pout, "w").write(behavior)
    send({"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"turn-%d" % n}]}})
    result = os.environ.get("STREAM_RESULT")
    if result and n == 1:
        send({"type":"result","result":result,"usage":{"input_tokens":3,"output_tokens":5}})
    else:
        send({"type":"result","result":"turn-%d" % n,"usage":{"input_tokens":1,"output_tokens":1}})
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunnerFromBinding_StreamRejectsPlaceholders(t *testing.T) {
	if _, err := RunnerFromBinding(InterfaceStream, "claude -p {system}"); err == nil {
		t.Fatal("expected error for {system} in stream command")
	}
	if _, err := RunnerFromBinding(InterfaceStream, "claude -p {payload}"); err == nil {
		t.Fatal("expected error for {payload} in stream command")
	}
	if _, err := RunnerFromBinding(InterfaceStream, "claude"); err == nil {
		t.Fatal("expected error for bare token")
	}
	if _, err := RunnerFromBinding(InterfaceStream, "in-loop"); err == nil {
		t.Fatal("expected error for in-loop")
	}
	r, err := RunnerFromBinding(InterfaceStream, DefaultClaudeStreamCommand)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Command(), "stream-json") {
		t.Errorf("Command() = %q", r.Command())
	}
	if strings.Contains(DefaultClaudeStreamCommand, "{system}") || strings.Contains(DefaultClaudeStreamCommand, "{payload}") {
		t.Fatal("DefaultClaudeStreamCommand must not carry {system}/{payload}")
	}
}

func TestStreamRunner_FakePeerDecision(t *testing.T) {
	skipWithoutPython3(t)
	peer := writeFakeStreamPeer(t)
	dir := t.TempDir()
	userOut := filepath.Join(dir, "user.txt")
	r, err := RunnerFromBinding(InterfaceStream, peer+" --output-format stream-json")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"decision":"accept","notes":"stream-ok"}`
	out, err := r.Run(context.Background(), Request{
		SystemPrompt: "rubric-system",
		Payload:      `{"story":{"id":"sty_stream"}}`,
		AllowedTools: "Read,Grep,Glob",
		Env:          map[string]string{"USER_OUT": userOut, "STREAM_RESULT": want},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(out) != want {
		t.Fatalf("unwrapped capture = %q, want %q", out, want)
	}
	gotUser, err := os.ReadFile(userOut)
	if err != nil {
		t.Fatalf("user out: %v", err)
	}
	if !strings.Contains(string(gotUser), "rubric-system") || !strings.Contains(string(gotUser), "sty_stream") {
		t.Fatalf("first user message missing system/payload: %q", gotUser)
	}
}

func TestStreamSession_SecondTurn(t *testing.T) {
	skipWithoutPython3(t)
	peer := writeFakeStreamPeer(t)
	dir := t.TempDir()
	mark := filepath.Join(dir, "mark.txt")
	r, err := newStreamRunner(peer + " --output-format stream-json")
	if err != nil {
		t.Fatal(err)
	}
	sr := r.(streamRunner)
	sess, err := openStreamSession(context.Background(), sr, Request{
		AllowedTools: "Read,Grep,Glob",
		Env:          map[string]string{"STREAM_MARK": mark},
	}, defaultPermissionPolicy(false))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	if err := sess.Send(context.Background(), Turn{System: "sys", Text: "one"}); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	got1 := waitEventText(t, sess, "turn-1")
	if err := sess.Send(context.Background(), Turn{Text: "two"}); err != nil {
		t.Fatalf("send 2: %v", err)
	}
	got2 := waitEventText(t, sess, "turn-2")
	if got1 != "turn-1" || got2 != "turn-2" {
		t.Fatalf("texts = %q %q", got1, got2)
	}
	b, err := os.ReadFile(mark)
	if err != nil {
		t.Fatalf("mark: %v", err)
	}
	if n := strings.Count(string(b), "start"); n != 1 {
		t.Fatalf("process started %d times, want 1", n)
	}
}

// TestStreamSession_EmitsSessionInitEvent (sty_7069bced): a stream-json
// transport's own system/init record — sent once, before any turn — names
// the model the session actually opened with. cmd_story_chat's orchestrator
// capture reads this to record the "orchestrator" session-model tier.
func TestStreamSession_EmitsSessionInitEvent(t *testing.T) {
	skipWithoutPython3(t)
	peer := writeFakeStreamPeer(t)
	r, err := newStreamRunner(peer + " --output-format stream-json")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var seen []Event
	initSeen := make(chan struct{}, 1)
	sess, err := openStreamSession(context.Background(), r.(streamRunner), Request{
		AllowedTools: "Read,Grep,Glob",
		Env:          map[string]string{"STREAM_INIT_MODEL": "claude-opus-5-5"},
		OnEvent: func(ev Event) {
			mu.Lock()
			seen = append(seen, ev)
			mu.Unlock()
			if ev.Kind == EventSessionInit {
				select {
				case initSeen <- struct{}{}:
				default:
				}
			}
		},
	}, defaultPermissionPolicy(false))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	select {
	case <-initSeen:
	case <-time.After(10 * time.Second):
		t.Fatal("no session_init event")
	}
	mu.Lock()
	defer mu.Unlock()
	var inits []Event
	for _, ev := range seen {
		if ev.Kind == EventSessionInit {
			inits = append(inits, ev)
		}
	}
	if len(inits) != 1 || inits[0].Model != "claude-opus-5-5" {
		t.Fatalf("session_init events = %#v, want exactly one with model claude-opus-5-5", inits)
	}
}

// TestStreamSession_ToolEventsFromContentBlocks (sty_1de7494c AC4): tools the
// binding pre-allowed raise no can_use_tool request, so their tool_use /
// tool_result content blocks are the only boundary — the session reports them
// as EventToolStart / EventToolEnd through the synchronous OnEvent path.
func TestStreamSession_ToolEventsFromContentBlocks(t *testing.T) {
	skipWithoutPython3(t)
	peer := writeFakeStreamPeer(t)
	r, err := newStreamRunner(peer + " --output-format stream-json")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var seen []Event
	done := make(chan struct{}, 1)
	sess, err := openStreamSession(context.Background(), r.(streamRunner), Request{
		AllowedTools: "Read,Grep,Glob",
		Env:          map[string]string{"STREAM_TOOL": "1"},
		OnEvent: func(ev Event) {
			mu.Lock()
			seen = append(seen, ev)
			mu.Unlock()
			if ev.Kind == EventCompleted {
				select {
				case done <- struct{}{}:
				default:
				}
			}
		},
	}, defaultPermissionPolicy(false))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.Send(context.Background(), Turn{System: "sys", Text: "one"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("no completed event")
	}
	mu.Lock()
	defer mu.Unlock()
	var got []string
	for _, ev := range seen {
		if ev.Kind == EventToolStart || ev.Kind == EventToolEnd {
			got = append(got, string(ev.Kind)+":"+ev.Tool+":"+ev.Status)
		}
	}
	want := []string{"tool_start:Read:running", "tool_end:Read:done", "tool_start:Bash:running", "tool_end:Bash:error"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tool events = %v, want %v", got, want)
	}
}

func TestStreamSession_PermissionDenyMutator(t *testing.T) {
	skipWithoutPython3(t)
	peer := writeFakeStreamPeer(t)
	dir := t.TempDir()
	permOut := filepath.Join(dir, "perm.txt")
	r, err := RunnerFromBinding(InterfaceStream, peer+" --output-format stream-json")
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background(), Request{
		SystemPrompt: "r",
		Payload:      "{}",
		AllowedTools: "Read,Grep,Glob",
		Env:          map[string]string{"STREAM_PERM": "1", "PERM_OUT": permOut, "STREAM_RESULT": `{"decision":"accept","notes":"ok"}`},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(permOut)
	if err != nil {
		t.Fatalf("perm out: %v", err)
	}
	if strings.TrimSpace(string(got)) != "deny" {
		t.Fatalf("behavior = %q, want deny", got)
	}
}

func TestStreamSession_PermissionAllowMutatorGrant(t *testing.T) {
	skipWithoutPython3(t)
	peer := writeFakeStreamPeer(t)
	dir := t.TempDir()
	permOut := filepath.Join(dir, "perm.txt")
	r, err := RunnerFromBinding(InterfaceStream, peer+" --output-format stream-json")
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background(), Request{
		SystemPrompt: "r",
		Payload:      "{}",
		AllowedTools: "Read,Edit,Write,Bash",
		Env:          map[string]string{"STREAM_PERM": "1", "PERM_OUT": permOut, "STREAM_RESULT": `{"decision":"accept","notes":"ok"}`},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(permOut)
	if err != nil {
		t.Fatalf("perm out: %v", err)
	}
	if strings.TrimSpace(string(got)) != "allow" {
		t.Fatalf("behavior = %q, want allow", got)
	}
}

func waitEventText(t *testing.T, sess Session, want string) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for event text %q", want)
			return ""
		case ev, ok := <-sess.Events():
			if !ok {
				t.Fatalf("events closed waiting for %q", want)
				return ""
			}
			if ev.Kind == EventMessage && strings.Contains(ev.Text, want) {
				return ev.Text
			}
		}
	}
}

func TestStreamResultJSONRoundTrip(t *testing.T) {
	// Fixture shape recorded from stream-json: a result event whose `result`
	// field is a JSON *string* wrapping the verdict. Captured() must unwrap it.
	raw := []byte(`{"type":"result","subtype":"success","result":"{\"decision\":\"accept\",\"notes\":\"stream-ok\"}","usage":{"input_tokens":1,"output_tokens":2}}`)
	var msg map[string]any
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	res, ok := msg["result"].(string)
	if !ok || !strings.Contains(res, `"decision":"accept"`) {
		t.Fatalf("fixture result = %#v", msg["result"])
	}
}
