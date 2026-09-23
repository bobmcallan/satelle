package agentstep

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
)

// writeFakeStreamPeer spawns a minimal stream-json peer (same protocol shape as
// agentcli's own fake-peer test harness, stream_test.go:24) that answers one
// turn with a bare decision and a `result` event carrying `modelUsage`, keyed
// by resolved model id — exactly what `claude --output-format stream-json`
// reports (sty_87b86044 evidence).
func writeFakeStreamPeer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-stream-peer")
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
    if msg.get("type") != "user":
        continue
    send({"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}})
    send({
        "type": "result",
        "result": "{\"decision\":\"accept\",\"notes\":\"stream-ok\"}",
        "usage": {"input_tokens": 3, "output_tokens": 11},
        "modelUsage": {
            "claude-opus-5-5": {"inputTokens": 3, "outputTokens": 11, "costUSD": 0.1065628}
        }
    })
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestStreamDispatch_ModelResolved pins AC2 end to end through the same seam a
// real reviewer/executor dispatch uses (g.Invoke, not the runOnce primitive):
// a gate run over the stream transport must record BOTH the configured alias
// ("model") and the resolved canonical id ("model_resolved") the stream-json
// `result` event's modelUsage reported, on the decision itself — not
// agentcli.ModelUnavailable. Before the underlying fix, runOnce discarded the
// session's EventUsage and ran agentcli.UnwrapUsage on the already-unwrapped
// decision text, which carries no modelUsage of its own, so every stream
// dispatch recorded model_resolved="unavailable".
func TestStreamDispatch_ModelResolved(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	peer := writeFakeStreamPeer(t)
	runner, err := agentcli.RunnerFromBinding(agentcli.InterfaceStream, peer+" --output-format stream-json")
	if err != nil {
		t.Fatal(err)
	}
	g := New(&fakeRunner{}, fakeDocs{}, t.TempDir(), "")
	const alias = "opus"
	res := g.Invoke(context.Background(), InvokeRequest{
		Binding: config.AgentBinding{Model: alias, Principles: config.PrinciplesNone},
		Section: "reviewer",
		Rubric:  "judge",
		Payload: map[string]string{"story": "sty_1"},
		Expect:  ExpectVerdict,
		Runner:  runner,
		Skill:   "test-skill",
	})
	if res.Err != nil {
		t.Fatalf("Invoke: %v", res.Err)
	}
	if res.Decision == nil {
		t.Fatal("Decision is nil — fake peer's result did not parse as a verdict")
	}
	if !res.Decision.Accept || res.Decision.Notes != "stream-ok" {
		t.Fatalf("unexpected decision: %+v", res.Decision)
	}
	if res.Decision.Model != alias {
		t.Errorf("Decision.Model = %q, want configured alias %q", res.Decision.Model, alias)
	}
	if res.Decision.ModelResolved != "claude-opus-5-5" {
		t.Fatalf("Decision.ModelResolved = %q, want %q", res.Decision.ModelResolved, "claude-opus-5-5")
	}
	if res.Decision.ModelResolved == agentcli.ModelUnavailable {
		t.Fatal("stream dispatch must not record the unavailable marker when modelUsage was reported")
	}
	if len(res.Decision.Models) != 1 || res.Decision.Models[0].ID != "claude-opus-5-5" || res.Decision.Models[0].TokensOut != 11 {
		t.Fatalf("Decision.Models = %+v, want one claude-opus-5-5 entry with 11 output tokens", res.Decision.Models)
	}
}
