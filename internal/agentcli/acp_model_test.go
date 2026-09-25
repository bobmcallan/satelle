package agentcli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeModelPeer is an ACP peer that appends every session/set_config_option
// params object to logPath and, when peerModel is non-empty, names it in its
// session/new reply.
func fakeModelPeer(t *testing.T, logPath, peerModel string) string {
	t.Helper()
	res := `{"sessionId":"sess_test"}`
	if peerModel != "" {
		res = fmt.Sprintf(`{"sessionId":"sess_test","models":{"currentModelId":%q}}`, peerModel)
	}
	script := `#!/usr/bin/env python3
import json, sys
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
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":mid,"result":` + res + `})
    elif method == "session/set_config_option":
        with open(` + fmt.Sprintf("%q", logPath) + `, "a") as f:
            f.write(json.dumps(msg.get("params")) + "\n")
        send({"jsonrpc":"2.0","id":mid,"result":{}})
    elif method == "session/prompt":
        send({"jsonrpc":"2.0","id":mid,"result":{"stopReason":"end_turn"}})
    elif mid is not None:
        send({"jsonrpc":"2.0","id":mid,"result":{}})
`
	path := filepath.Join(t.TempDir(), "fake-model-peer")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func acpInitModels(t *testing.T, peerModel, reqModel string) (inits []string, setLog string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "cfg.log")
	r, err := RunnerFromBinding(InterfaceACP, fakeModelPeer(t, logPath, peerModel)+" stdio")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	_, err = r.Run(context.Background(), Request{
		SystemPrompt: "rubric",
		Payload:      `{}`,
		Model:        reqModel,
		OnEvent: func(ev Event) {
			if ev.Kind == EventSessionInit {
				mu.Lock()
				inits = append(inits, ev.Model)
				mu.Unlock()
			}
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	b, _ := os.ReadFile(logPath)
	mu.Lock()
	defer mu.Unlock()
	return inits, string(b)
}

// TestACPSessionInitReportsAppliedModel: the peer is handed the requested
// model through session/set_config_option, and one EventSessionInit carries
// the model the session started with (sty_bb92973f).
func TestACPSessionInitReportsAppliedModel(t *testing.T) {
	inits, setLog := acpInitModels(t, "", "grok-4.5")
	if !strings.Contains(setLog, `"configId": "model"`) || !strings.Contains(setLog, `"value": "grok-4.5"`) {
		t.Fatalf("peer set_config_option log = %q, want model=grok-4.5", setLog)
	}
	if len(inits) != 1 || inits[0] != "grok-4.5" {
		t.Fatalf("session inits = %v, want exactly [grok-4.5]", inits)
	}
}

// TestACPSessionInitPrefersPeerReportedModel: the id the peer named on
// session/new is what ran.
func TestACPSessionInitPrefersPeerReportedModel(t *testing.T) {
	inits, _ := acpInitModels(t, "peer-model-1", "")
	if len(inits) != 1 || inits[0] != "peer-model-1" {
		t.Fatalf("session inits = %v, want exactly [peer-model-1]", inits)
	}
}

// TestACPSessionInitEmptyWhenModelUnknown: no request and no peer model is an
// explicit empty model, never a default.
func TestACPSessionInitEmptyWhenModelUnknown(t *testing.T) {
	inits, setLog := acpInitModels(t, "", "")
	if setLog != "" {
		t.Fatalf("unexpected model config sent: %q", setLog)
	}
	if len(inits) != 1 || inits[0] != "" {
		t.Fatalf("session inits = %v, want exactly [\"\"]", inits)
	}
}
