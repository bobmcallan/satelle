//go:build integration

// Black-box coverage for sty_8e0b29a0: `satelle story rework` relays a coder
// session and a CONSULTING reviewer session through the real built binary,
// against two live stream peers, and — whatever the outcome — leaves the story's
// status exactly where it found it. Convergence is a signal; the gate decides.
package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reworkStreamPeer writes a stream-json peer that answers each user turn with
// the next entry of a "|"-separated script read from a file, so the same binary
// serves both sides of the relay with different scripts.
func reworkStreamPeer(t *testing.T, path, scriptFile string) {
	t.Helper()
	body := `#!/usr/bin/env python3
import json, sys
replies = open(` + pyQuote(scriptFile) + `).read().split("|")
n = 0
def send(o):
    sys.stdout.write(json.dumps(o) + "\n"); sys.stdout.flush()
while True:
    line = sys.stdin.readline()
    if not line:
        break
    if json.loads(line).get("type") != "user":
        continue
    text = replies[n] if n < len(replies) else "(exhausted)"
    n += 1
    send({"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":text}]}})
    send({"type":"result","result":text})
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func pyQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

// reworkFixture builds a repo whose in_progress step is performed by a live
// [coder] and declares `rework = { consult = "consultant", rounds = 2 }`.
// Returns the repo, the story id, and the two script paths to write replies to.
func reworkFixture(t *testing.T) (repo, id, coderScript, consultScript string) {
	t.Helper()
	repo = t.TempDir()
	mustRun(t, testBin, repo, "init")
	seedCodeSkill(t, repo)

	coderScript = filepath.Join(repo, "coder-replies.txt")
	consultScript = filepath.Join(repo, "consult-replies.txt")
	coderPeer := filepath.Join(repo, "peer-coder")
	consultPeer := filepath.Join(repo, "peer-consultant")
	reworkStreamPeer(t, coderPeer, coderScript)
	reworkStreamPeer(t, consultPeer, consultScript)
	// The engaging transition dispatches the coder once (agent=coder), before the
	// relay runs, so both scripts must already exist.
	writeFile(t, coderScript, "dispatch ack")
	writeFile(t, consultScript, "NOT READY: unused")

	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.toml"),
		"[meta]\nname = \"done\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"rework relay fixture\"\n\n"+
			"[\"*\"]\nobligations = [\"raised\", \"coded\", \"closed\"]\n")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.toml"),
		"[meta]\nname = \"step\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"rework relay fixture\"\n\n"+
			"[raised]\nstatus = \"backlog\"\nstart = true\n\n"+
			"[coded]\nstatus = \"in_progress\"\nagent = \"coder\"\nskills = [\"code\"]\nrequires = [\"raised\"]\n"+
			"rework = { consult = \"consultant\", rounds = 2 }\n\n"+
			"[closed]\nstatus = \"done\"\nterminal = true\nrequires = [\"coded\"]\n\n"+
			"[[gate]]\nskill = \"satelle-estimate-actual-review\"\non = [\"__never__\"]\n")

	agents := filepath.Join(repo, ".satelle", "workflows", "agents.toml")
	f, err := os.OpenFile(agents, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(
		"\n[coder]\nrole = \"agent\"\ninterface = \"stream\"\n" +
			"command = \"" + coderPeer + " --output-format stream-json\"\n" +
			"tools = \"Read,Grep,Glob,Edit,Write,Bash(satelle:*)\"\nmodel = \"opus\"\n" +
			"\n[consultant]\nrole = \"reviewer\"\ninterface = \"stream\"\n" +
			"command = \"" + consultPeer + " --output-format stream-json\"\n" +
			"tools = \"Read,Grep,Glob\"\nmodel = \"opus\"\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "Converge before the gate",
		"--body", "a coder and a consulting reviewer converse, then the gate decides",
		"--acceptance", "1. the relay converges and never moves status")
	id = extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	return repo, id, coderScript, consultScript
}

// TestReworkRelayOnTheRouteAndInTheVerb: the authored key is visible on the
// route, the relay converges through two live peers, the transcript reads as
// the conversation with its real roles, and the status never moves.
func TestReworkRelayOnTheRouteAndInTheVerb(t *testing.T) {
	repo, id, coderScript, consultScript := reworkFixture(t)

	// AC1: the route SHOWS the loop — "config over code" is only real if the
	// operator can see the process without opening a workflow file.
	route := mustRun(t, testBin, repo, "story", "route", id)
	for _, want := range []string{"rework: consult consultant", "up to 2 round(s)", "satelle story rework"} {
		if !strings.Contains(route, want) {
			t.Errorf("story route missing %q:\n%s", want, route)
		}
	}

	writeFile(t, consultScript, "AC1 has no test.\nNOT READY: AC1 has no test|Proven now.\nREADY")
	writeFile(t, coderScript, "added the AC1 test; done")

	before := mustRun(t, testBin, repo, "story", "get", id)
	out := mustRun(t, testBin, repo, "story", "rework", id)
	after := mustRun(t, testBin, repo, "story", "get", id)

	if !strings.Contains(out, `"converged":true`) || !strings.Contains(out, `"rounds":2`) {
		t.Fatalf("relay did not converge in two rounds:\n%s", out)
	}
	// AC3: status is identical before and after. The relay is not an authority.
	if !strings.Contains(before, `"status": "in_progress"`) || !strings.Contains(after, `"status": "in_progress"`) {
		t.Fatalf("the relay moved status:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// AC2: the conversation, in order, with the real roles and the wildcard
	// audience that carries it to whoever judges the edge.
	msgs := mustRun(t, testBin, repo, "story", "messages", id)
	var rows []struct {
		From string `json:"from"`
		To   string `json:"to"`
		Cc   string `json:"cc"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(msgs), &rows); err != nil {
		t.Fatalf("parse messages: %v\n%s", err, msgs)
	}
	if len(rows) != 3 {
		t.Fatalf("message rows = %d, want 3 (consult, coder, consult):\n%s", len(rows), msgs)
	}
	wantDir := [][2]string{{"consultant", "coder"}, {"coder", "consultant"}, {"consultant", "coder"}}
	for i, w := range wantDir {
		if rows[i].From != w[0] || rows[i].To != w[1] {
			t.Errorf("row %d = %s -> %s, want %s -> %s", i, rows[i].From, rows[i].To, w[0], w[1])
		}
		if rows[i].Cc != "*" {
			t.Errorf("row %d cc = %q, want *", i, rows[i].Cc)
		}
	}

	// The outcome is also ledgered, so the orchestrator can read it either way.
	led := mustRun(t, testBin, repo, "ledger", "list", "--story", id)
	if !strings.Contains(led, "rework converged=true rounds=2/2") {
		t.Errorf("ledger missing the relay outcome row:\n%s", led)
	}
}

// TestReworkRelaySpentBudgetDoesNotPark: the budget runs out, converged is
// false with the last objection captured — and the story is STILL in_progress.
// Parking is the orchestrator's decision, per the consultation principle, not
// the relay's.
func TestReworkRelaySpentBudgetDoesNotPark(t *testing.T) {
	repo, id, coderScript, consultScript := reworkFixture(t)
	writeFile(t, consultScript, "NOT READY: one|NOT READY: two|NOT READY: three")
	writeFile(t, coderScript, "tried|tried again|tried once more")

	out := mustRun(t, testBin, repo, "story", "rework", id)
	if !strings.Contains(out, `"converged":false`) || !strings.Contains(out, `"rounds":2`) {
		t.Fatalf("relay result wrong:\n%s", out)
	}
	if !strings.Contains(out, `"last_objection":"two"`) {
		t.Fatalf("last objection not captured:\n%s", out)
	}
	got := mustRun(t, testBin, repo, "story", "get", id)
	if !strings.Contains(got, `"status": "in_progress"`) {
		t.Fatalf("a spent budget must not move or park the story:\n%s", got)
	}
}

// TestReworkAbsentKeyRefusesByName: a step that declares no loop has no loop,
// and the verb says so rather than inventing one — the AC1 no-op guarantee at
// the command surface.
func TestReworkAbsentKeyRefusesByName(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	seedCodeSkill(t, repo)
	writeCoderPerformingRoute(t, repo)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "No loop here", "--body", "this step declares no rework", "--acceptance", "1. refused by name")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}
	got, err := run(t, testBin, repo, "story", "rework", id)
	if err == nil {
		t.Fatalf("want a refusal for a step with no rework key:\n%s", got)
	}
	if !strings.Contains(got, "declares no rework loop") {
		t.Errorf("refusal must name the missing key:\n%s", got)
	}
}
