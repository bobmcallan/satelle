//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// brokenWildcardLane seeds the substrate-lane route with a wildcard lane that
// dispatches a planner and a coder, and gives both seats pi-named tools under a
// Claude command in agents.local.toml: the id4me / satelle-server shape, where a
// profile change moved the seats to Claude and the local file kept pi's names.
func brokenWildcardLane(t *testing.T, repo string) {
	t.Helper()
	done := strings.Replace(substrateLaneDone,
		`obligations = ["raised", "coded", "closed"]`,
		`obligations = ["raised", "planned", "coded", "closed"]`, 1)
	step := strings.Replace(substrateLaneStep,
		"[coded]\nstatus = \"in_progress\"\nagent = \"executor\"\n",
		"[planned]\nstatus = \"plan\"\nagent = \"planner\"\nrequires = [\"raised\"]\n\n"+
			"[coded]\nstatus = \"in_progress\"\nagent = \"coder\"\n", 1)
	step = strings.Replace(step, "reviewer_agent = \"reviewer\"\nrequires = [\"raised\"]\n\n[closed]",
		"reviewer_agent = \"reviewer\"\nrequires = [\"planned\"]\n\n[closed]", 1)
	if done == substrateLaneDone || !strings.Contains(step, `agent = "planner"`) || !strings.Contains(step, `requires = ["planned"]`) {
		t.Fatal("fixture rewrite did not apply: the substrate-lane fixture changed shape")
	}
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.toml"), done)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.toml"), step)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.local.toml"),
		"[planner]\nrole = \"agent\"\ncommand = \""+agentcli.DefaultClaudeCommand+"\"\ntools = \"read,grep,find,ls\"\n\n"+
			"[coder]\nrole = \"agent\"\ncommand = \""+agentcli.DefaultClaudeCommand+"\"\ntools = \"read,bash,edit,write\"\n")
}

// A node.alloc failure on the * lane's planner and coder does not stop a
// substrate story, whose own lane dispatches neither, from engaging and closing;
// a story the * lane governs is still refused (sty_259572f6 AC1). Doctor and
// `satelle agent validate` name the repo, the seat and the pi names (AC2).
func TestBrokenWildcardSeatsDoNotBlockSubstrateLane(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	brokenWildcardLane(t, repo)
	materializeDefault(t, repo, "skills", "satelle-substrate-only-check")
	stubReviewerAccept(t, repo)
	mustRun(t, testBin, repo, "reindex")

	// Precondition: the fixture reaches the defect — the unscoped check fails on
	// the * lane's planner and coder nodes.
	validateOut, verr := run(t, testBin, repo, "agent", "validate")
	if verr == nil || !strings.Contains(validateOut, "no context channel") ||
		!strings.Contains(validateOut, "agent=planner") || !strings.Contains(validateOut, "agent=coder") {
		t.Fatalf("agent validate must report the * lane's planner and coder have no context channel:\n%s", validateOut)
	}

	// AC2: both surfaces name the repo, the file, the seat and the names.
	doctorOut, _ := run(t, testBin, repo, "doctor")
	for name, out := range map[string]string{"agent validate": validateOut, "doctor": doctorOut} {
		for _, want := range []string{repo, "agents.local.toml [planner]", "read,grep,find,ls", "agents.local.toml [coder]", "read,bash,edit,write"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s must name %q:\n%s", name, want, out)
			}
		}
	}

	gitInit(t, repo)
	gitCommitAll(t, repo, "initial scaffold")

	// A story the * lane governs walks the broken seats and is refused.
	bug := extractID(mustRun(t, testBin, repo, "story", "create", "--title", "Fix a widget",
		"--category", "bug", "--body", "The widget does not render.", "--acceptance", "1. the widget renders"), "sty_")
	if out, err := run(t, testBin, repo, "story", "set", bug, "--status", "plan"); err == nil ||
		!strings.Contains(out, "node.alloc") || !strings.Contains(out, "agent=planner") {
		t.Fatalf("a * lane story must be refused with node.alloc naming the planner node:\n%s", out)
	}

	// The substrate story engages and closes on its own lane.
	sid := extractID(mustRun(t, testBin, repo, "story", "create", "--title", "Restate local tools",
		"--category", "substrate", "--body", "Restate agents.local.toml tools in Claude's names."), "sty_")
	if out, err := run(t, testBin, repo, "story", "set", sid, "--status", "in_progress"); err != nil {
		t.Fatalf("a substrate story must engage while the * lane's seats are broken: %v\n%s", err, out)
	}
	docPath := filepath.Join(repo, "docs", "repair.md")
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte("# repair\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, repo, "docs: repair ("+sid+")")
	if out, err := run(t, testBin, repo, "story", "set", sid, "--status", "done"); err != nil {
		t.Fatalf("the substrate story must close: %v\n%s", err, out)
	}
}

// brokenWildcardNoSubstrate is brokenWildcardLane with the [substrate] table
// removed from done.toml: the petitio / portside / vire-infra shape, where a
// substrate story falls to the * lane and its broken planner and coder seats.
func brokenWildcardNoSubstrate(t *testing.T, repo string) {
	t.Helper()
	brokenWildcardLane(t, repo)
	path := filepath.Join(repo, ".satelle", "workflows", "done.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(body), "\n[substrate]\n")
	if i < 0 {
		t.Fatal("fixture rewrite did not apply: the substrate-lane fixture has no [substrate] table")
	}
	writeFile(t, path, string(body[:i+1]))
}

// agentsRepairRepo is an initialised, committed repo with no [substrate] lane
// and a broken * lane, where the agents-repair lane and its close check resolve
// as the binary ships them (nothing materialised).
func agentsRepairRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	brokenWildcardNoSubstrate(t, repo)
	stubReviewerAccept(t, repo)
	mustRun(t, testBin, repo, "reindex")
	gitInit(t, repo)
	gitCommitAll(t, repo, "initial scaffold")
	return repo
}

func createAgentsRepair(t *testing.T, repo string) string {
	t.Helper()
	return extractID(mustRun(t, testBin, repo, "story", "create", "--title", "Restate local tools in Claude's names",
		"--category", "agents-repair", "--body", "agents.local.toml grants pi tool names under a Claude command."), "sty_")
}

// With no [substrate] table and a broken * lane, an agents-repair story engages,
// makes its repair through the edit gate and closes through `satelle story set`
// alone (sty_32431a16 AC1); a story the * lane governs — bug or substrate — is
// still refused with node.alloc naming the planner (AC2).
func TestBrokenWildcardSeatsAgentsRepairLane(t *testing.T) {
	repo := agentsRepairRepo(t)

	// AC2: both * lane stories walk the broken seats and are refused.
	for _, cat := range []string{"bug", "substrate"} {
		id := extractID(mustRun(t, testBin, repo, "story", "create", "--title", "A "+cat+" story",
			"--category", cat, "--body", "Walks the * lane.", "--acceptance", "1. it is refused"), "sty_")
		if out, err := run(t, testBin, repo, "story", "set", id, "--status", "plan"); err == nil ||
			!strings.Contains(out, "node.alloc") || !strings.Contains(out, "agent=planner") {
			t.Fatalf("a %s story governed by * must be refused with node.alloc naming agent=planner:\n%s", cat, out)
		}
	}

	// AC1: the agents-repair story engages on its own lane.
	sid := createAgentsRepair(t, repo)
	if out, err := run(t, testBin, repo, "story", "set", sid, "--status", "in_progress"); err != nil {
		t.Fatalf("an agents-repair story must engage while the * lane's seats are broken: %v\n%s", err, out)
	}
	local := filepath.Join(repo, ".satelle", "workflows", "agents.local.toml")
	if out, ok := gateEventOut(t, repo, local); !ok {
		t.Fatalf("the edit gate must allow the agents-repair story's binding edit:\n%s", out)
	}
	writeFile(t, local,
		"[planner]\nrole = \"agent\"\ncommand = \""+agentcli.DefaultClaudeCommand+"\"\ntools = \"Read,Grep,Glob,Bash(satelle:*)\"\n\n"+
			"[coder]\nrole = \"agent\"\ncommand = \""+agentcli.DefaultClaudeCommand+"\"\ntools = \"Read,Edit,Write,Bash(satelle:*)\"\n")
	if out, err := presentDone(t, repo, sid); err != nil {
		t.Fatalf("the agents-repair story must close: %v\n%s", err, out)
	}
	if out, _ := run(t, testBin, repo, "story", "get", sid); !strings.Contains(out, `"status": "done"`) {
		t.Fatalf("the agents-repair story must be done:\n%s", out)
	}
	// The repair is real: the * lane's seats now have a context channel.
	if out, _ := run(t, testBin, repo, "agent", "validate"); strings.Contains(out, "no context channel") {
		t.Errorf("after the repair the * lane's seats must have a context channel:\n%s", out)
	}
}

// The lane carries an agents-layer repair and nothing else: a change set with
// any other path, or none at all, is refused at done by
// satelle-agents-layer-only-check, and the story stays in_progress (sty_32431a16
// AC3).
func TestAgentsRepairLaneRefusesOtherPaths(t *testing.T) {
	t.Run("other path", func(t *testing.T) {
		repo := agentsRepairRepo(t)
		sid := createAgentsRepair(t, repo)
		mustRun(t, testBin, repo, "story", "set", sid, "--status", "in_progress")
		// Written directly: this case judges the close gate, not the edit gate.
		writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.local.toml"), "[planner]\nrole = \"agent\"\n")
		writeFile(t, filepath.Join(repo, ".satelle", "skills", "x.md"), "# x\n")
		out, err := presentDone(t, repo, sid)
		if err == nil || !strings.Contains(out, "satelle-agents-layer-only-check") || !strings.Contains(out, ".satelle/skills/x.md") {
			t.Fatalf("an agents-repair close with another path must be refused by satelle-agents-layer-only-check naming it:\n%s", out)
		}
		if got, _ := run(t, testBin, repo, "story", "get", sid); !strings.Contains(got, `"status": "in_progress"`) {
			t.Fatalf("the refused story must stay in_progress:\n%s", got)
		}
	})
	t.Run("empty change set", func(t *testing.T) {
		repo := agentsRepairRepo(t)
		sid := createAgentsRepair(t, repo)
		mustRun(t, testBin, repo, "story", "set", sid, "--status", "in_progress")
		out, err := presentDone(t, repo, sid)
		if err == nil || !strings.Contains(out, "no change set found") {
			t.Fatalf("an agents-repair close with an empty change set must be refused:\n%s", out)
		}
	})
}
