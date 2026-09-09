package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

func TestChatLoopStreamFakeProcess(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	peer := filepath.Join(dir, "fake-stream-peer")
	script := `#!/usr/bin/env python3
import json, sys
def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()
def read():
    line = sys.stdin.readline()
    return json.loads(line) if line else None
n = 0
while True:
    msg = read()
    if msg is None:
        break
    if msg.get("type") != "user":
        continue
    n += 1
    send({"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"stream-reply-%d" % n}]}})
    send({"type":"result","result":"stream-reply-%d" % n})
`
	if err := os.WriteFile(peer, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	opener, err := agentcli.OpenerFromBinding(agentcli.InterfaceStream, peer+" --output-format stream-json")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := opener(context.Background(), agentcli.Request{
		SystemPrompt: "sys",
		Payload:      "{}",
		AllowedTools: "Read,Grep,Glob",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	loop := &chatLoop{Sess: sess, StoryID: "sty_x"}
	in := strings.NewReader("hello\n/quit\n")
	var out bytes.Buffer
	if err := loop.Run(context.Background(), in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "stream-reply-") {
		t.Fatalf("stdout missing stream reply: %q", out.String())
	}
}
