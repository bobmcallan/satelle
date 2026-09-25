package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
)

// TestMain re-execs this test binary as the satelle CLI when
// SATELLE_CLI_REEXEC=1, so an end-to-end test can hand a child process a real
// `satelle` without building one (sty_1de7494c AC5). Every other run falls
// straight through to the package's tests.
func TestMain(m *testing.M) {
	if os.Getenv("SATELLE_CLI_REEXEC") == "1" {
		root := NewRootCmd()
		root.SetArgs(os.Args[1:])
		c, err := root.ExecuteC()
		if c != nil {
			closeAppForCmd(c)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	// Isolate gate/seat tests from the AMBIENT process environment: when this
	// test binary itself runs under a real satelle dispatch (a named-agent
	// step, or — as when this test was written — a rework-relay coder session
	// working on THIS story), SATELLE_DISPATCH_*/SATELLE_RELAY_* are already
	// set on the host process. hookDenyReason and friends read them via
	// os.Getenv with no test-injection seam, so an unrelated gate test that
	// never calls t.Setenv would otherwise silently pick up a REAL dispatch
	// marker and take the relay-deny branch instead of the plain one it
	// asserts on (sty_752c4ef2 rework-relay review round). Clear them once
	// here so every test starts from a clean marker state; a test that wants
	// one sets it itself via t.Setenv.
	for _, k := range []string{
		config.DispatchAgentEnv, config.DispatchStepEnv, config.DispatchItemEnv,
		config.RelayBindingEnv, config.RelayItemEnv,
	} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}

// TestStoryChatE2EChildDispatchInheritsSeat (AC5): the orchestrator session is a
// real stream process; its scripted turn shells the real `satelle story set`
// against the same repo. The transition must go through the ordinary
// synchronous dispatch path (unchanged by chat), the child must inherit the
// chat session's seat via SATELLE_SESSION (not acquire a second one), and the
// whole exchange must land on the story ledger.
func TestStoryChatE2EChildDispatchInheritsSeat(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	repo := tempRepo(t)
	t.Chdir(repo)

	// A three-performing-step lane with no reviewers: backlog → in_progress →
	// integration → done. The child moves in_progress → integration.
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	writeRoute(t, wfDir,
		`["*"]
obligations = ["raised", "coded", "integrated", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[integrated]
status = "integration"
agent = "executor"
requires = ["coded"]

[closed]
status = "done"
terminal = true
requires = ["integrated"]
`)

	// The fake orchestrator: on its first user turn it runs the real CLI (this
	// test binary re-exec'd) to advance the story, reports the boundary as a
	// tool_use / tool_result pair, and answers with the child's exit code.
	peer := filepath.Join(t.TempDir(), "fake-orchestrator")
	script := `#!/usr/bin/env python3
import json, os, subprocess, sys
def send(o):
    sys.stdout.write(json.dumps(o) + "\n"); sys.stdout.flush()
def read():
    line = sys.stdin.readline()
    return json.loads(line) if line else None
while True:
    msg = read()
    if msg is None:
        break
    if msg.get("type") != "user":
        continue
    env = dict(os.environ); env["SATELLE_CLI_REEXEC"] = "1"
    p = subprocess.run([os.environ["E2E_EXE"], "story", "set", os.environ["E2E_STORY"], "--status", "integration"],
                       env=env, capture_output=True, text=True)
    open(os.environ["E2E_OUT"], "w").write(p.stdout + "\n" + p.stderr)
    send({"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"satelle story set"}}]}})
    send({"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"rc=%d" % p.returncode}]}})
    send({"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"child-rc=%d" % p.returncode}]}})
    send({"type":"result","result":"child-rc=%d" % p.returncode})
`
	if err := os.WriteFile(peer, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	agents := "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n" +
		"[orchestrator]\nrole = \"agent\"\ninterface = \"stream\"\n" +
		"command = \"" + peer + " --output-format stream-json\"\n" +
		"tools = \"Read,Grep,Glob,Bash(satelle:*)\"\n"
	if err := os.WriteFile(filepath.Join(wfDir, config.AgentsConfigName), []byte(agents), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(repo, ".satelle", config.AgentsConfigName)) // legacy location seeded by tempRepo

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DocIndex.Sync(context.Background(), map[string]string{"workflows": wfDir}, time.Now().UTC()); err != nil {
		_ = db.Close()
		t.Fatalf("doc sync: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "story", "create",
		"--title", "chat e2e",
		"--body", "the orchestrator advances me from inside its session",
		"--acceptance", "1. status advanced by a child satelle under the chat seat",
		"--category", "chore",
	)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created map[string]any
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id in %s", out)
	}

	// The chat session's identity. `story set` below stamps the seat with it;
	// OpenOrchestrator exports it to the peer; the peer's child inherits it.
	t.Setenv(config.SessionEnv, "sess-e2e")
	if out, err := runRoot(t, "story", "set", id, "--status", "in_progress"); err != nil {
		t.Fatalf("engage: %v\n%s", err, out)
	}

	childOut := filepath.Join(t.TempDir(), "child.txt")
	t.Setenv("E2E_EXE", exe)
	t.Setenv("E2E_STORY", id)
	t.Setenv("E2E_OUT", childOut)

	chatOut, err := runRootIn(t, "go\n/quit\n", "story", "chat", id)
	childLog, _ := os.ReadFile(childOut)
	if err != nil {
		t.Fatalf("chat: %v\n%s\nchild: %s", err, chatOut, childLog)
	}
	if !strings.Contains(chatOut, "child-rc=0") {
		t.Fatalf("child satelle failed under the chat seat:\n%s\nchild: %s", chatOut, childLog)
	}

	// Status advanced through the ordinary path.
	got, err := runRoot(t, "story", "get", id)
	if err != nil {
		t.Fatalf("get: %v\n%s", err, got)
	}
	if !strings.Contains(got, `"status": "integration"`) {
		t.Fatalf("status did not advance to integration:\n%s\nchild: %s", got, childLog)
	}

	// The seat carried through: same story, now at integration, not a second
	// seat and not dropped by the child.
	seat, err := runRoot(t, "story", "seat")
	if err != nil {
		t.Fatalf("seat: %v\n%s", err, seat)
	}
	if !strings.Contains(seat, id) || !strings.Contains(seat, `"state": "integration"`) {
		t.Fatalf("seat should be held on %s at integration:\n%s", id, seat)
	}

	// The exchange is on the ledger: human turn, orchestrator reply, and the
	// tool boundary the child ran behind (synchronous sink, not the render loop).
	led, err := runRoot(t, "ledger", "list", "--story", id)
	if err != nil {
		t.Fatalf("ledger: %v\n%s", err, led)
	}
	for _, want := range []string{`"agent_message"`, `"go"`, `child-rc=0`, `"agent_invocation"`, `chat start Bash by session`, `chat end Bash by session`} {
		if !strings.Contains(led, want) {
			t.Errorf("ledger missing %s:\n%s", want, led)
		}
	}

	// AC2 (sty_a0372443): SATELLE_SESSION is set here, so the driving side is an
	// AGENT — `story messages` must show both directions with developer-agent as
	// the counterpart, not the literal "human".
	msgs, err := runRoot(t, "story", "messages", id)
	if err != nil {
		t.Fatalf("messages: %v\n%s", err, msgs)
	}
	assertMessagePair(t, msgs, "developer-agent", "orchestrator")
}

// assertMessagePair checks `story messages` carries a from→to row and its
// to→from reply — the two ledger directions a chat turn writes.
func assertMessagePair(t *testing.T, msgs, from, to string) {
	t.Helper()
	var rows []struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal([]byte(msgs), &rows); err != nil {
		t.Fatalf("parse messages: %v\n%s", err, msgs)
	}
	var fwd, back bool
	for _, r := range rows {
		if r.From == from && r.To == to {
			fwd = true
		}
		if r.From == to && r.To == from {
			back = true
		}
	}
	if !fwd || !back {
		t.Fatalf("want both %s -> %s and %s -> %s in:\n%s", from, to, to, from, msgs)
	}
}

// TestStoryChatE2EConsultationRoles (sty_a0372443 AC1/AC2): `story chat --agent
// <binding> --from <role>` opens the NAMED binding — not [orchestrator] — and
// both ledger directions carry the chosen roles.
func TestStoryChatE2EConsultationRoles(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	repo := tempRepo(t)
	t.Chdir(repo)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	writeRoute(t, wfDir,
		`["*"]
obligations = ["raised", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[closed]
status = "done"
terminal = true
requires = ["raised"]
`)

	// A consultant that answers by naming the charter line it was given, so the
	// test proves the CONSULTING binding (not the orchestrator) was opened.
	peer := filepath.Join(t.TempDir(), "fake-consultant")
	script := `#!/usr/bin/env python3
import json, os, sys
def send(o):
    sys.stdout.write(json.dumps(o) + "\n"); sys.stdout.flush()
while True:
    line = sys.stdin.readline()
    if not line:
        break
    msg = json.loads(line)
    if msg.get("type") != "user":
        continue
    send({"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"consulted-ok"}]}})
    send({"type":"result","result":"consulted-ok"})
`
	if err := os.WriteFile(peer, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	agents := "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n" +
		"[orchestrator]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n" +
		"[consultant]\nrole = \"reviewer\"\ninterface = \"stream\"\n" +
		"command = \"" + peer + " --output-format stream-json\"\n" +
		"tools = \"Read,Grep,Glob\"\n"
	if err := os.WriteFile(filepath.Join(wfDir, config.AgentsConfigName), []byte(agents), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(repo, ".satelle", config.AgentsConfigName))

	out, err := runRoot(t, "story", "create",
		"--title", "chat consultation e2e",
		"--body", "the in-repo agent consults a reviewer binding",
		"--acceptance", "1. the consulted binding answers and both directions are ledgered",
		"--category", "chore",
	)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created map[string]any
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id in %s", out)
	}

	// [orchestrator] is in-loop here: without --agent chat must refuse, and with
	// --agent consultant it must open the live consultant instead.
	if _, err := runRootIn(t, "/quit\n", "story", "chat", id); err == nil {
		t.Fatal("chat with an in-loop [orchestrator] must refuse")
	}

	chatOut, err := runRootIn(t, "why did you reject\n/quit\n",
		"story", "chat", id, "--agent", "consultant", "--from", "developer-agent")
	if err != nil {
		t.Fatalf("chat --agent consultant: %v\n%s", err, chatOut)
	}
	if !strings.Contains(chatOut, "consulted-ok") {
		t.Fatalf("consultant reply missing:\n%s", chatOut)
	}

	msgs, err := runRoot(t, "story", "messages", id)
	if err != nil {
		t.Fatalf("messages: %v\n%s", err, msgs)
	}
	assertMessagePair(t, msgs, "developer-agent", "consultant")
}

// TestStoryChatE2EAskDeniedIsLedgered (sty_ff50f788): a live `story chat`
// session whose agent asks the user writes an agent-interactive-denied row
// through the CLI's own engine wiring, not only an engine a test hand-wires.
func TestStoryChatE2EAskDeniedIsLedgered(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	repo := tempRepo(t)
	t.Chdir(repo)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	writeRoute(t, wfDir,
		`["*"]
obligations = ["raised", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[closed]
status = "done"
terminal = true
requires = ["raised"]
`)

	// A consultant that asks the user before it answers.
	peer := filepath.Join(t.TempDir(), "fake-asker")
	script := `#!/usr/bin/env python3
import json, sys
def send(o):
    sys.stdout.write(json.dumps(o) + "\n"); sys.stdout.flush()
while True:
    line = sys.stdin.readline()
    if not line:
        break
    msg = json.loads(line)
    if msg.get("type") != "user":
        continue
    send({"type":"control_request","request_id":"ask1","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{"questions":[{"question":"which file first?"}]}}})
    sys.stdin.readline()
    send({"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"asked-ok"}]}})
    send({"type":"result","result":"asked-ok"})
`
	if err := os.WriteFile(peer, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	agents := "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n" +
		"[orchestrator]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n" +
		"[consultant]\nrole = \"reviewer\"\ninterface = \"stream\"\n" +
		"command = \"" + peer + " --output-format stream-json\"\n" +
		"tools = \"Read,Grep,Glob\"\n"
	if err := os.WriteFile(filepath.Join(wfDir, config.AgentsConfigName), []byte(agents), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(repo, ".satelle", config.AgentsConfigName))

	out, err := runRoot(t, "story", "create",
		"--title", "chat ask e2e",
		"--body", "the consulted agent asks the user",
		"--acceptance", "1. the denied ask is ledgered",
		"--category", "chore",
	)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created map[string]any
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id in %s", out)
	}

	chatOut, err := runRootIn(t, "go\n/quit\n", "story", "chat", id, "--agent", "consultant")
	if err != nil {
		t.Fatalf("chat: %v\n%s", err, chatOut)
	}
	if !strings.Contains(chatOut, "asked-ok") {
		t.Fatalf("consultant reply missing:\n%s", chatOut)
	}

	led, err := runRoot(t, "ledger", "list", "--story", id)
	if err != nil {
		t.Fatalf("ledger: %v\n%s", err, led)
	}
	for _, want := range []string{"agent-interactive-denied", "AskUserQuestion", "which file first?", `"response": "denied"`} {
		if !strings.Contains(led, want) {
			t.Errorf("ledger missing %q:\n%s", want, led)
		}
	}
}
