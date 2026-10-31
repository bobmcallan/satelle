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
