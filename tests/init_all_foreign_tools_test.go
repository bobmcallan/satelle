//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One `satelle init --all --yes` repairs a registered repo whose
// agents.local.toml grants its planner and coder pi's tool names under the
// Claude CLI (sty_f7f80c48 AC3), and the dry run before it lists each rewrite
// and changes nothing (AC4).
func TestInitAllRepairsForeignSeatTools(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	brokenWildcardLane(t, repo)
	materializeDefault(t, repo, "skills", "satelle-substrate-only-check")
	stubReviewerAccept(t, repo)
	mustRun(t, testBin, repo, "reindex")
	local := filepath.Join(repo, ".satelle", "workflows", "agents.local.toml")

	// Each surface's marks of the fault: the node.alloc refusal (agent validate
	// prints its text, doctor its identifier) and the agents.binding warning.
	marks := map[string][]string{
		"agent validate": {"no context channel", "restates tools in pi's names"},
		"doctor":         {"[node.alloc]", "no context channel", "restates tools in pi's names"},
	}
	surfaces := func() map[string]string {
		v, _ := run(t, testBin, repo, "agent", "validate")
		d, _ := run(t, testBin, repo, "doctor")
		return map[string]string{"agent validate": v, "doctor": d}
	}

	// Precondition: the registered repo carries the fault on both surfaces.
	for name, out := range surfaces() {
		for _, want := range marks[name] {
			if !strings.Contains(out, want) {
				t.Fatalf("precondition: %s must report %q:\n%s", name, want, out)
			}
		}
	}

	// AC4: the dry run lists each rewrite and writes no byte.
	before := sha256File(t, local)
	dry, err := run(t, testBin, repo, "init", "--all")
	if err != nil {
		t.Fatalf("init --all (dry run): %v\n%s", err, dry)
	}
	for _, want := range []string{
		"STALE   " + repo,
		".satelle/workflows/agents.local.toml [agents]: [planner] tools read,grep,find,ls → ",
		".satelle/workflows/agents.local.toml [agents]: [coder] tools read,bash,edit,write → ",
		"--yes",
	} {
		if !strings.Contains(dry, want) {
			t.Errorf("the dry run must list %q:\n%s", want, dry)
		}
	}
	if sha256File(t, local) != before {
		t.Errorf("the dry run must not rewrite agents.local.toml")
	}

	// AC3: --yes repairs it, and neither surface reports the fault after.
	healed, err := run(t, testBin, repo, "init", "--all", "--yes")
	if err != nil || !strings.Contains(healed, "HEALED  "+repo) {
		t.Fatalf("init --all --yes must heal the repo: %v\n%s", err, healed)
	}
	body, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "read,") {
		t.Errorf("agents.local.toml must carry no pi names after the heal:\n%s", body)
	}
	after := surfaces()
	for name, out := range after {
		for _, gone := range append(marks[name], "node.alloc", "agents.binding") {
			if strings.Contains(out, gone) {
				t.Errorf("%s must not report %q after init --all --yes:\n%s", name, gone, out)
			}
		}
	}
	if out, err := run(t, testBin, repo, "agent", "validate"); err != nil {
		t.Errorf("agent validate must pass after the heal: %v\n%s", err, out)
	}
}
