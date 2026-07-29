package agentinstall

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// TestCodexACP_InstalledLauncher (sty_9e86f407 AC5): drive the *installed*
// launcher + generated binding through RunnerFromBinding with a fake npx peer.
// Covers init, model/effort config, CaptureAnswer/Full, mutation deny, and
// permitted Satelle read operations.
func TestCodexACP_InstalledLauncher(t *testing.T) {
	home := t.TempDir()
	rs, err := Install(home, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("install: %+v", rs)
	}
	launcher := rs[0].Path
	snip := BindingSnippet("codex", launcher)
	cmdLine := extractCommandFromSnippet(t, snip)
	if strings.Contains(cmdLine, "stdio") {
		t.Fatalf("snippet command has stdio: %s", cmdLine)
	}
	// Must invoke the installed launcher (not only raw npx).
	if !strings.Contains(cmdLine, launcher) {
		t.Fatalf("binding command must include launcher path %q, got %q", launcher, cmdLine)
	}
	body, _ := os.ReadFile(launcher)
	if strings.Contains(string(body), "stdio") {
		t.Fatalf("launcher must not bake stdio: %s", body)
	}

	// --- CaptureAnswer + model/effort + no stdio argv ---
	t.Run("init_effort_capture_answer", func(t *testing.T) {
		binDir, argvLog, cfgLog, _ := setupFakeNpx(t, "")
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		r, err := agentcli.RunnerFromBinding(agentcli.InterfaceACP, cmdLine)
		if err != nil {
			t.Fatal(err)
		}
		out, err := r.Run(context.Background(), agentcli.Request{
			SystemPrompt: "rubric",
			Payload:      `{}`,
			Effort:       "high",
			Model:        "gpt-5.6-terra",
			AllowedTools: "read_file,grep,list_dir",
			Capture:      agentcli.CaptureAnswer,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !strings.Contains(string(out), `"decision":"accept"`) {
			t.Fatalf("stdout = %q", out)
		}
		cfg := mustRead(t, cfgLog)
		if !strings.Contains(cfg, "reasoning_effort") || !strings.Contains(cfg, "high") {
			t.Fatalf("want effort set_config_option:\n%s", cfg)
		}
		if !strings.Contains(cfg, "model") || !strings.Contains(cfg, "gpt-5.6-terra") {
			t.Fatalf("want model set_config_option:\n%s", cfg)
		}
		argv := mustRead(t, argvLog)
		if strings.Contains(argv, "stdio") {
			t.Fatalf("must not pass stdio argv:\n%s", argv)
		}
		if strings.Contains(argv, "--reasoning-effort") {
			t.Fatalf("must not get --reasoning-effort argv:\n%s", argv)
		}
	})

	// --- CaptureFull keeps narration + decision ---
	t.Run("capture_full", func(t *testing.T) {
		extra := `
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Codex narration before tools"}}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"tool_call","toolCallId":"c1","title":"list_dir","kind":"read"}}})
        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"completed"}}})
`
		binDir, _, _, _ := setupFakeNpx(t, extra)
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		r, err := agentcli.RunnerFromBinding(agentcli.InterfaceACP, cmdLine)
		if err != nil {
			t.Fatal(err)
		}
		full, err := r.Run(context.Background(), agentcli.Request{
			SystemPrompt: "r", Payload: `{}`, AllowedTools: "read_file,grep,list_dir",
			Capture: agentcli.CaptureFull,
		})
		if err != nil {
			t.Fatalf("CaptureFull: %v", err)
		}
		if !strings.Contains(string(full), "Codex narration") {
			t.Fatalf("CaptureFull dropped narration: %q", full)
		}
		if !strings.Contains(string(full), `"decision":"accept"`) {
			t.Fatalf("CaptureFull dropped decision: %q", full)
		}
		ans, err := r.Run(context.Background(), agentcli.Request{
			SystemPrompt: "r", Payload: `{}`, AllowedTools: "read_file,grep,list_dir",
			Capture: agentcli.CaptureAnswer,
		})
		if err != nil {
			t.Fatalf("CaptureAnswer: %v", err)
		}
		if strings.Contains(string(ans), "Codex narration") {
			t.Fatalf("CaptureAnswer leaked narration: %q", ans)
		}
	})

	// --- Mutation denied under read-only tools ---
	t.Run("deny_mutation", func(t *testing.T) {
		extra := `
        send({"jsonrpc":"2.0","id":99,"method":"session/request_permission","params":{"sessionId":"sess_test","toolCall":{"toolCallId":"c1","kind":"edit","title":"Write"},"options":[{"optionId":"allow-once","name":"Allow","kind":"allow_once"},{"optionId":"reject-once","name":"Reject","kind":"reject_once"}]}})
        resp = read()
        if resp is None or resp.get("result",{}).get("outcome",{}).get("optionId") != "reject-once":
            send({"jsonrpc":"2.0","id":mid,"error":{"code":1,"message":"expected reject-once, got %s" % (resp,)}})
            continue
`
		binDir, _, _, _ := setupFakeNpx(t, extra)
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		r, err := agentcli.RunnerFromBinding(agentcli.InterfaceACP, cmdLine)
		if err != nil {
			t.Fatal(err)
		}
		out, err := r.Run(context.Background(), agentcli.Request{
			SystemPrompt: "r", Payload: `{}`, AllowedTools: "read_file,grep,list_dir",
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !strings.Contains(string(out), "accept") {
			t.Fatalf("out = %q", out)
		}
	})

	// --- Permitted Satelle read op under Bash(satelle:*) ---
	t.Run("permit_satelle_read", func(t *testing.T) {
		extra := `
        send({"jsonrpc":"2.0","id":99,"method":"session/request_permission","params":{"sessionId":"sess_test","toolCall":{"toolCallId":"c1","kind":"read","title":"Read"},"options":[{"optionId":"allow-once","name":"Allow","kind":"allow_once"},{"optionId":"reject-once","name":"Reject","kind":"reject_once"}]}})
        resp = read()
        if resp is None or resp.get("result",{}).get("outcome",{}).get("optionId") != "allow-once":
            send({"jsonrpc":"2.0","id":mid,"error":{"code":1,"message":"expected allow-once for read, got %s" % (resp,)}})
            continue
`
		binDir, _, _, _ := setupFakeNpx(t, extra)
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		r, err := agentcli.RunnerFromBinding(agentcli.InterfaceACP, cmdLine)
		if err != nil {
			t.Fatal(err)
		}
		out, err := r.Run(context.Background(), agentcli.Request{
			SystemPrompt: "r", Payload: `{}`,
			AllowedTools: "read_file,grep,list_dir,Bash(satelle:*)",
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !strings.Contains(string(out), "accept") {
			t.Fatalf("out = %q", out)
		}
	})
}

func extractCommandFromSnippet(t *testing.T, snip string) string {
	t.Helper()
	for _, line := range strings.Split(snip, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "command") {
			continue
		}
		i := strings.Index(line, "=")
		if i < 0 {
			continue
		}
		v := strings.TrimSpace(line[i+1:])
		v = strings.Trim(v, `"`)
		if v != "" {
			return v
		}
	}
	t.Fatalf("no command in snippet:\n%s", snip)
	return ""
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// setupFakeNpx writes a fake npx + ACP peer; returns binDir, argvLog, cfgLog, peer path.
func setupFakeNpx(t *testing.T, extra string) (binDir, argvLog, cfgLog, peer string) {
	t.Helper()
	binDir = t.TempDir()
	argvLog = filepath.Join(t.TempDir(), "argv.log")
	cfgLog = filepath.Join(t.TempDir(), "cfg.log")
	peer = writeInstalledACPPeer(t, cfgLog, extra)
	npx := filepath.Join(binDir, "npx")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvLog + "\nexec " + peer + "\n"
	if err := os.WriteFile(npx, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binDir, argvLog, cfgLog, peer
}

func writeInstalledACPPeer(t *testing.T, cfgLog, extra string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "codex-peer")
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
            f.write("initialize protocolVersion=%s\n" % (params.get("protocolVersion"),))
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
