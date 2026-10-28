package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

// sty_4470bb38: a story whose coder is a one-shot command seat, rejected at its
// step's exit, gets the coder back by `story set <id> --status <current>`.

// redispatchRepo is a repo whose in_progress step is allocated to a [coder] with
// interface=command (a logging stub that answers rd-gate#1) and declares a rework
// loop, and whose integration step is gated by a check that rejects its first
// presentation and writes each payload it is handed to dir. The story is engaged
// at in_progress.
func redispatchRepo(t *testing.T) (dir, id string) {
	t.Helper()
	repo := tempRepo(t)
	t.Chdir(repo)
	dir = t.TempDir()
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	writeRoute(t, wfDir,
		`["*"]
obligations = ["raised", "coded", "integrated", "closed"]
park = { state = "blocked" }
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "coder"
requires = ["raised"]
rework = { consult = "consultant", rounds = 2 }

[integrated]
status = "integration"
reviewers = ["rd-gate"]
requires = ["coded"]

[closed]
status = "done"
terminal = true
requires = ["integrated"]
`)
	skills := filepath.Join(repo, ".satelle", "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(dir, "gate-count")
	gate := "---\nname: rd-gate\ntype: skill\ndescription: scripted exit gate for the re-dispatch tests\n---\n\n```check\n" +
		"n=$(cat " + counter + " 2>/dev/null || echo 0)\nn=$((n+1))\necho $n > " + counter + "\n" +
		"cat > " + filepath.Join(dir, "gate-") + "$n.json\n" +
		"if [ \"$n\" -le 1 ]; then echo \"REJECT-NOTE-MARKER: the guard allows a downgrade\"; exit 1; fi\nexit 0\n```\n"
	if err := os.WriteFile(filepath.Join(skills, "rd-gate.md"), []byte(gate), 0o644); err != nil {
		t.Fatal(err)
	}
	coder := filepath.Join(dir, "fake-coder")
	script := "#!/bin/sh\n{ echo \"=== RUN\"; cat; echo; } >> " + filepath.Join(dir, "coder.log") + "\n" +
		`echo '{"answers":[{"finding":"rd-gate#1","answer":"adopted","evidence":"guard refuses the downgrade"}]}'` + "\n"
	if err := os.WriteFile(coder, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tools := "tools = \"Read,Grep,Glob,Edit,Write,Bash(satelle:*)\"\n"
	agents := "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n" +
		"[orchestrator]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n" +
		"[coder]\nrole = \"agent\"\ninterface = \"command\"\ncommand = \"" + coder + " -p {system}\"\n" + tools + "\n" +
		"[consultant]\nrole = \"reviewer\"\ninterface = \"stream\"\ncommand = \"" + coder + " --output-format stream-json\"\n" +
		"tools = \"Read,Grep,Glob\"\nisolation = \"operator-attested\"\n"
	if err := os.WriteFile(filepath.Join(wfDir, config.AgentsConfigName), []byte(agents), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(repo, ".satelle", config.AgentsConfigName))
	if out, err := runRoot(t, "reindex"); err != nil {
		t.Fatalf("reindex: %v\n%s", err, out)
	}
	out, err := runRoot(t, "story", "create", "--title", "re-dispatch me", "--body", "a command coder answers its exit gate",
		"--acceptance", "1. the coder is re-dispatched", "--category", "chore")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}
	id = created["id"].(string)
	if out, err := runRoot(t, "story", "set", id, "--status", "in_progress"); err != nil {
		t.Fatalf("engage: %v\n%s", err, out)
	}
	return dir, id
}

// coderRuns is each payload the coder stub was handed, in order.
func coderRuns(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "coder.log"))
	if err != nil {
		return nil
	}
	return strings.Split(string(b), "=== RUN")[1:]
}

// AC1–AC3 end to end: the exit gate rejects, the orchestrator leaves the coder a
// message, and the same-status set dispatches the coder once with the gate's
// finding and the message in its payload; the next presentation's gate is told
// the finding was answered. A repeated set dispatches nothing, and status never
// moves on either.
func TestSameStatusSetRedispatchesACommandCoderWithTheExitFindings(t *testing.T) {
	dir, id := redispatchRepo(t)
	if n := len(coderRuns(t, dir)); n != 1 {
		t.Fatalf("the coder runs once on entry, ran %d times", n)
	}
	if out, err := runRoot(t, "story", "set", id, "--status", "integration"); err == nil {
		t.Fatalf("the exit gate must reject its first presentation:\n%s", out)
	}
	if out, err := runRoot(t, "story", "message", id, "--from", "orchestrator", "--to", "coder",
		"--body", "ORCH-DECISION-MARKER: keep the guard"); err != nil {
		t.Fatalf("message: %v\n%s", err, out)
	}

	if out, err := runRoot(t, "story", "set", id, "--status", "in_progress"); err != nil {
		t.Fatalf("same-status set: %v\n%s", err, out)
	}
	runs := coderRuns(t, dir)
	if len(runs) != 2 {
		t.Fatalf("the same-status set must dispatch the coder exactly once more, ran %d times", len(runs))
	}
	for _, want := range []string{`"findings_to_answer"`, `"rd-gate#1"`, "REJECT-NOTE-MARKER", `"findings_edge_to":"integration"`, "ORCH-DECISION-MARKER"} {
		if !strings.Contains(runs[1], want) {
			t.Errorf("the re-dispatched payload must carry %s:\n%s", want, runs[1])
		}
	}
	if got := storyStatus(t, id); got != "in_progress" {
		t.Fatalf("status = %q, the re-dispatch never moves status", got)
	}

	if out, err := runRoot(t, "story", "set", id, "--status", "in_progress"); err != nil {
		t.Fatalf("repeated set: %v\n%s", err, out)
	}
	if n := len(coderRuns(t, dir)); n != 2 {
		t.Errorf("a repeated set with no newer reject must dispatch nothing, coder ran %d times", n)
	}
	if got := storyStatus(t, id); got != "in_progress" {
		t.Fatalf("status = %q after a repeated set", got)
	}

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	inv, err := db.Ledger.ListByStory(context.Background(), id, ledger.KindAgentInvocation)
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	steps := 0
	for _, e := range inv {
		var p struct{ From, To string }
		if json.Unmarshal(e.Payload, &p) == nil && p.From == "in_progress" && p.To == "in_progress" {
			steps++
		}
	}
	if steps != 1 {
		t.Errorf("in_progress→in_progress agent_invocation rows = %d, want 1", steps)
	}

	if out, err := runRoot(t, "story", "set", id, "--status", "integration"); err != nil {
		t.Fatalf("re-present the exit: %v\n%s", err, out)
	}
	gate, err := os.ReadFile(filepath.Join(dir, "gate-2.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		FindingAnswers *struct {
			Answered []struct {
				Finding string `json:"finding"`
			} `json:"answered"`
			Missing []string `json:"missing"`
		} `json:"finding_answers"`
	}
	if err := json.Unmarshal(gate, &payload); err != nil {
		t.Fatalf("gate payload: %v\n%s", err, gate)
	}
	if fa := payload.FindingAnswers; fa == nil || len(fa.Answered) != 1 || fa.Answered[0].Finding != "rd-gate#1" || len(fa.Missing) != 0 {
		t.Errorf("the exit gate must be told rd-gate#1 was answered: %+v\n%s", fa, gate)
	}
}

// AC5: a coder seat that is not live-capable is refused by `story rework` with
// the one-shot re-dispatch named, so the operator is not left without a way out.
func TestStoryReworkRefusalNamesTheOneShotRedispatch(t *testing.T) {
	_, id := redispatchRepo(t)
	out, err := runRoot(t, "story", "rework", id)
	if err == nil {
		t.Fatalf("a command-interface coder cannot be relayed:\n%s", out)
	}
	for _, want := range []string{"interface=command", "satelle story set " + id + " --status in_progress"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must name %q, got: %v", want, err)
		}
	}
}
