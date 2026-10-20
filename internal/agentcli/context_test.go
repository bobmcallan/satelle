package agentcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The context-source rows (sty_12499ecc AC2): every adapter and transport either
// renders the declared sources and admits exactly their allow-listed tools, or
// fails the run with an error naming the adapter and transport — before anything
// is spawned. The source name "src" is a neutral test name; no vendor appears.

const testSecret = "s3cret-token-value"

func testSources() []ContextServer {
	return []ContextServer{{
		Name: "src", Type: ContextHTTP, URL: "https://ctx.example.test/mcp",
		Headers: map[string]string{"Authorization": "Bearer " + testSecret},
		Tools:   []string{"get_issue", "search_issues"},
	}}
}

// fakeBinary writes an executable shell script named base into its own dir.
func fakeBinary(t *testing.T, base, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fakes")
	}
	path := filepath.Join(t.TempDir(), base)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// argvRecorder is a fake harness that logs its argv, one argument per line.
func argvRecorder(t *testing.T, base string) (bin, log string) {
	t.Helper()
	log = filepath.Join(t.TempDir(), "argv.log")
	bin = fakeBinary(t, base, `printf '%s\n' "$@" > '`+log+`'
printf '%s' '{"result":"ok"}'
`)
	return bin, log
}

func readArgv(t *testing.T, log string) []string {
	t.Helper()
	var b []byte
	var err error
	for i := 0; i < 100; i++ {
		if b, err = os.ReadFile(log); err == nil && len(b) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("harness never ran (no argv log): %v", err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func assertClaudeRendered(t *testing.T, args []string) {
	t.Helper()
	cfgRaw, ok := flagValue(args, "--mcp-config")
	if !ok {
		t.Fatalf("no --mcp-config in %v", args)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(cfgRaw), &cfg); err != nil {
		t.Fatalf("--mcp-config is not JSON: %v: %s", err, cfgRaw)
	}
	src, ok := cfg.MCPServers["src"]
	if !ok || src.Type != "http" || src.URL != "https://ctx.example.test/mcp" || src.Headers["Authorization"] != "Bearer "+testSecret {
		t.Fatalf("mcpServers = %+v", cfg.MCPServers)
	}
	allowed, _ := flagValue(args, "--allowedTools")
	for _, want := range []string{"Read", "mcp__src__get_issue", "mcp__src__search_issues"} {
		if !strings.Contains(allowed, want) {
			t.Errorf("--allowedTools %q missing %s", allowed, want)
		}
	}
	if strings.Contains(allowed, "mcp__src__delete_issue") {
		t.Errorf("--allowedTools admits an undeclared tool: %q", allowed)
	}
	// --tools names built-in tools only: the MCP names must not leak into it.
	if tools, _ := flagValue(args, "--tools"); strings.Contains(tools, "mcp__") {
		t.Errorf("--tools = %q carries an MCP name", tools)
	}
	if !hasFlag(args, "--strict-mcp-config") {
		t.Errorf("--strict-mcp-config dropped: %v", args)
	}
}

func TestContext_ClaudeCommand_RendersSources(t *testing.T) {
	bin, log := argvRecorder(t, "claude")
	r, err := RunnerFromBinding(InterfaceCommand, bin+strings.TrimPrefix(DefaultClaudeCommand, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background(), Request{
		ReadOnly: true, AllowedTools: "Read,Grep", SystemPrompt: "s", Payload: "{}", ContextServers: testSources(),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertClaudeRendered(t, readArgv(t, log))
	if strings.Contains(r.Command(), testSecret) {
		t.Errorf("Command() evidence carries a header secret: %q", r.Command())
	}
}

func TestContext_ClaudeStream_RendersSources(t *testing.T) {
	log := filepath.Join(t.TempDir(), "argv.log")
	bin := fakeBinary(t, "claude", `printf '%s\n' "$@" > '`+log+`'
cat > /dev/null
`)
	r, err := RunnerFromBinding(InterfaceStream, bin+strings.TrimPrefix(DefaultClaudeStreamCommand, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	sr, ok := r.(streamRunner)
	if !ok {
		t.Fatalf("runner = %T", r)
	}
	sess, err := sr.Open(context.Background(), Request{
		ReadOnly: true, AllowedTools: "Read,Grep", ContextServers: testSources(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	assertClaudeRendered(t, readArgv(t, log))
	if strings.Contains(r.Command(), testSecret) {
		t.Errorf("Command() evidence carries a header secret: %q", r.Command())
	}
}

// A request with no sources renders exactly what it did before.
func TestContext_ClaudeCommand_NoSourcesUnchanged(t *testing.T) {
	bin, log := argvRecorder(t, "claude")
	r, err := RunnerFromBinding(InterfaceCommand, bin+strings.TrimPrefix(DefaultClaudeCommand, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), Request{ReadOnly: true, AllowedTools: "Read,Grep", SystemPrompt: "s", Payload: "{}"}); err != nil {
		t.Fatal(err)
	}
	args := readArgv(t, log)
	if hasFlag(args, "--mcp-config") {
		t.Errorf("--mcp-config rendered with no sources: %v", args)
	}
	if allowed, _ := flagValue(args, "--allowedTools"); allowed != "Read,Grep" {
		t.Errorf("--allowedTools = %q", allowed)
	}
}

// The unsupported rows fail before spawn with an error naming adapter/transport,
// and never leak the header secret.
func TestContext_UnsupportedRowsFailBeforeSpawn(t *testing.T) {
	cases := []struct {
		row, base, iface, command, want string
	}{
		{"grok/command", "grok", InterfaceCommand, strings.TrimPrefix(DefaultGrokCommand, "grok"), "grok/command"},
		{"grok/stream", "grok", InterfaceStream, " --input-format stream-json --output-format stream-json --tools read_file", "grok/stream"},
		{"cursor/command", "cursor-agent", InterfaceCommand, strings.TrimPrefix(CursorCommand, "cursor-agent"), "cursor/command"},
		{"pi/command", "pi", InterfaceCommand, " -p {payload}", "pi/command"},
		{"pi/stream", "pi", InterfaceStream, " --mode json --tools read", "pi/stream"},
		{"unknown/command", "mystery-harness", InterfaceCommand, " -p {payload}", "unknown/command"},
		{"unknown/stream", "mystery-harness", InterfaceStream, " --stream", "unknown/stream"},
		{"unknown/acp", "mystery-harness", InterfaceACP, " stdio", "unknown/acp"},
	}
	for _, tc := range cases {
		t.Run(tc.row, func(t *testing.T) {
			bin, log := argvRecorder(t, tc.base)
			r, err := RunnerFromBinding(tc.iface, bin+tc.command)
			if err != nil {
				t.Fatalf("runner: %v", err)
			}
			_, err = r.Run(context.Background(), Request{ReadOnly: true, AllowedTools: "Read", SystemPrompt: "s", Payload: "{}", ContextServers: testSources()})
			if err == nil {
				t.Fatal("want a named error, got nil")
			}
			if !strings.Contains(err.Error(), "context sources not supported by "+tc.want) {
				t.Errorf("error %q does not name %s", err, tc.want)
			}
			if strings.Contains(err.Error(), testSecret) {
				t.Errorf("error leaks a header secret: %q", err)
			}
			if _, statErr := os.Stat(log); statErr == nil {
				t.Errorf("%s: the harness was spawned despite the unsupported row", tc.row)
			}
		})
	}
}

// cursor over stream cannot even be built (no stream-json input), and the support
// table names it too.
func TestContext_CursorStreamAndCloudRows(t *testing.T) {
	if _, err := RunnerFromBinding(InterfaceStream, "cursor-agent --foo bar"); err == nil {
		t.Fatal("cursor stream must not build")
	}
	for _, tc := range []struct{ adapter, transport string }{
		{HarnessCursor, InterfaceStream},
		{HarnessCursor, InterfaceCommand},
		{HarnessClaude, ContextTransportCloud},
		{HarnessGrok, ContextTransportCloud},
		{HarnessUnknown, ContextTransportCloud},
	} {
		err := ContextSupport(tc.adapter, tc.transport)
		if err == nil || !strings.Contains(err.Error(), tc.adapter+"/"+tc.transport) {
			t.Errorf("ContextSupport(%s,%s) = %v, want an error naming the row", tc.adapter, tc.transport, err)
		}
	}
	for _, tc := range []struct{ adapter, transport string }{
		{HarnessClaude, InterfaceCommand}, {HarnessClaude, InterfaceStream},
		{HarnessGrok, InterfaceACP}, {HarnessCursor, InterfaceACP}, {HarnessPi, InterfaceACP},
	} {
		if err := ContextSupport(tc.adapter, tc.transport); err != nil {
			t.Errorf("ContextSupport(%s,%s) = %v, want supported", tc.adapter, tc.transport, err)
		}
	}
}

// An undeclared MCP tool stays denied, whether the sources are declared or not;
// a declared one is admitted by exact name only.
func TestContext_UndeclaredMCPToolStillDenied(t *testing.T) {
	grant := "Read,Grep,Glob"
	with := ReviewerPermissionPolicyWithContext(grant, HarnessClaude, testSources())
	without := ReviewerPermissionPolicy(grant)
	for _, tc := range []struct {
		tool        string
		with, plain bool
	}{
		{"mcp__src__get_issue", true, false},
		{"mcp__src__search_issues", true, false},
		{"mcp__src__delete_issue", false, false}, // same server, tool not allow-listed
		{"mcp__other__get_issue", false, false},  // undeclared server
		{"MCP__SRC__GET_ISSUE", true, false},     // names are compared case-insensitively
		{"Read", true, true},
		{"Bash", false, false},
	} {
		if got := with(PermissionRequest{ToolName: tc.tool}).Allow; got != tc.with {
			t.Errorf("with sources: %s allowed=%v, want %v", tc.tool, got, tc.with)
		}
		if got := without(PermissionRequest{ToolName: tc.tool}).Allow; got != tc.plain {
			t.Errorf("without sources: %s allowed=%v, want %v", tc.tool, got, tc.plain)
		}
	}
}

// fakeContextPeer is an ACP peer that advertises (or not) http MCP servers,
// records session/new's params, asks permission for each tool in FAKE_CALLS and
// reports the outcomes in its decision.
const fakeContextPeer = `#!/usr/bin/env python3
import json, os, sys

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

def read():
    line = sys.stdin.readline()
    return json.loads(line) if line else None

while True:
    msg = read()
    if msg is None:
        break
    mid = msg.get("id")
    method = msg.get("method")
    if method == "initialize":
        caps = {"mcpCapabilities": {"http": os.environ.get("FAKE_MCP_HTTP") == "1"}}
        send({"jsonrpc":"2.0","id":mid,"result":{"protocolVersion":1,"agentCapabilities":caps,"authMethods":[]}})
    elif method == "session/new":
        with open(os.environ["FAKE_LOG"], "w") as f:
            json.dump(msg["params"], f)
        send({"jsonrpc":"2.0","id":mid,"result":{"sessionId":"sess_test","modes":{"currentModeId":"default","availableModes":[{"id":"default"}]}}})
    elif method == "session/prompt":
        outcomes = []
        for i, name in enumerate([c for c in os.environ.get("FAKE_CALLS", "").split(",") if c]):
            send({"jsonrpc":"2.0","id":100+i,"method":"session/request_permission","params":{"sessionId":"sess_test","toolCall":{"toolCallId":"c%d" % i,"kind":"other","title":name},"options":[{"optionId":"allow","name":"Allow","kind":"allow_once"},{"optionId":"reject","name":"Reject","kind":"reject_once"}]}})
            resp = read()
            outcomes.append(name + "=" + resp["result"]["outcome"]["optionId"])
        text = json.dumps({"decision":"accept","notes":";".join(outcomes)})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":text}}}})
        send({"jsonrpc":"2.0","id":mid,"result":{"stopReason":"end_turn"}})
    elif mid is not None:
        send({"jsonrpc":"2.0","id":mid,"result":{}})
`

func acpPeer(t *testing.T, base string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("python peer")
	}
	path := filepath.Join(t.TempDir(), base)
	if err := os.WriteFile(path, []byte(fakeContextPeer), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestContext_ACPRows_RenderMCPServersAndAdmitExactTools(t *testing.T) {
	for _, tc := range []struct {
		row, base, args string
		readOnly        bool
	}{
		{"grok/acp", "grok-peer", " agent stdio", true},
		{"cursor/acp", "cursor-agent", " acp", false},
		{"pi/acp", "pi", " acp", false},
	} {
		t.Run(tc.row, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "session-new.json")
			r, err := RunnerFromBinding(InterfaceACP, acpPeer(t, tc.base)+tc.args)
			if err != nil {
				t.Fatal(err)
			}
			req := Request{
				ReadOnly: tc.readOnly, AllowedTools: "read_file,grep,list_dir", SystemPrompt: "s", Payload: "{}",
				ContextServers: testSources(),
				Env:            map[string]string{"FAKE_MCP_HTTP": "1", "FAKE_LOG": log},
			}
			if tc.readOnly {
				req.Env["FAKE_CALLS"] = "mcp__src__get_issue,mcp__src__delete_issue,mcp__other__get_issue"
			}
			out, err := r.Run(context.Background(), req)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			b, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			var params struct {
				MCPServers []struct {
					Type    string `json:"type"`
					Name    string `json:"name"`
					URL     string `json:"url"`
					Headers []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"headers"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal(b, &params); err != nil {
				t.Fatal(err)
			}
			if len(params.MCPServers) != 1 {
				t.Fatalf("mcpServers = %s", b)
			}
			s := params.MCPServers[0]
			if s.Type != "http" || s.Name != "src" || s.URL != "https://ctx.example.test/mcp" ||
				len(s.Headers) != 1 || s.Headers[0].Name != "Authorization" || s.Headers[0].Value != "Bearer "+testSecret {
				t.Errorf("server = %+v", s)
			}
			if tc.readOnly {
				for _, want := range []string{
					"mcp__src__get_issue=allow", "mcp__src__delete_issue=reject", "mcp__other__get_issue=reject",
				} {
					if !strings.Contains(string(out), want) {
						t.Errorf("outcomes %s missing %s", out, want)
					}
				}
			}
		})
	}
}

// Without sources the session still offers the empty list it always did, and a
// reviewer denies every MCP call.
func TestContext_ACP_NoSourcesNoServersAndMCPDenied(t *testing.T) {
	log := filepath.Join(t.TempDir(), "session-new.json")
	r, err := RunnerFromBinding(InterfaceACP, acpPeer(t, "grok-peer")+" agent stdio")
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(context.Background(), Request{
		ReadOnly: true, AllowedTools: "read_file", SystemPrompt: "s", Payload: "{}",
		Env: map[string]string{"FAKE_LOG": log, "FAKE_CALLS": "mcp__src__get_issue"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), `"mcpServers": []`) {
		t.Errorf("session/new = %s, want an empty mcpServers", b)
	}
	if !strings.Contains(string(out), "mcp__src__get_issue=reject") {
		t.Errorf("outcomes = %s, want the undeclared MCP call rejected", out)
	}
}

// A peer that does not advertise http MCP servers cannot carry an http source:
// the run fails with an error naming adapter/acp and never leaks the secret.
func TestContext_ACP_PeerWithoutHTTPCapabilityIsNamedError(t *testing.T) {
	log := filepath.Join(t.TempDir(), "session-new.json")
	r, err := RunnerFromBinding(InterfaceACP, acpPeer(t, "grok-peer")+" agent stdio")
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background(), Request{
		ReadOnly: true, AllowedTools: "read_file", SystemPrompt: "s", Payload: "{}",
		ContextServers: testSources(), Env: map[string]string{"FAKE_LOG": log},
	})
	if err == nil || !strings.Contains(err.Error(), "context sources not supported by grok/acp") {
		t.Fatalf("err = %v, want a grok/acp named error", err)
	}
	if strings.Contains(err.Error(), testSecret) {
		t.Errorf("error leaks a header secret: %q", err)
	}
	if _, statErr := os.Stat(log); statErr == nil {
		t.Error("session/new was sent despite the unsupported capability")
	}
}

// A stdio source needs no advertised capability.
func TestContext_ACP_StdioSourceNeedsNoCapability(t *testing.T) {
	log := filepath.Join(t.TempDir(), "session-new.json")
	r, err := RunnerFromBinding(InterfaceACP, acpPeer(t, "grok-peer")+" agent stdio")
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background(), Request{
		ReadOnly: true, AllowedTools: "read_file", SystemPrompt: "s", Payload: "{}",
		ContextServers: []ContextServer{{Name: "local", Type: ContextStdio, Command: "ctx-server", Args: []string{"--ro"}, Tools: []string{"get"}}},
		Env:            map[string]string{"FAKE_LOG": log},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), `"command": "ctx-server"`) || !strings.Contains(string(b), `"--ro"`) {
		t.Errorf("session/new = %s", b)
	}
}
