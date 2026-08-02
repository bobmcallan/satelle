//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin sty_540cfcd3: `agent validate`, `validate` and `doctor` ran the
// same checks over DIFFERENT sources — the first two read the SQLite doc index,
// doctor read the authored files — so seconds apart, on an unchanged tree, they
// contradicted each other. Editing substrate is exactly when the index lags, and
// validating right after an edit is exactly what an author does.
//
// Every test here deliberately does NOT reindex after writing.

// writeAgentsWithPlanner appends a [planner] binding to the repo's agents.toml.
func writeAgentsWithPlanner(t *testing.T, repo string) {
	t.Helper()
	path := filepath.Join(repo, ".satelle", "workflows", "agents.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, []byte("\n[planner]\nrole = \"agent\"\n"+
		"command = \"claude -p --append-system-prompt {system} --allowedTools {tools} --model {model}\"\n"+
		"tools = \"Read,Grep,Glob,Bash(satelle:*)\"\nmodel = \"opus\"\n")...)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeRouteAllocatingPlanner writes a route whose plan step allocates
// agent: planner, directly to disk.
func writeRouteAllocatingPlanner(t *testing.T, repo string) {
	t.Helper()
	writeSpineFixture(t, repo, "", "", "",
		"plan|planner|||",
		"in_progress|executor|||",
		"done||||")
}

// TestAgentValidateAndDoctorAgreeWithoutReindex (AC2) is the exact reported
// case: a binding added to agents.toml and allocated by a workflow node, with no
// reindex in between. `agent validate` warned the binding was orphaned while
// `doctor` simultaneously reported that same node allocating it.
func TestAgentValidateAndDoctorAgreeWithoutReindex(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	writeAgentsWithPlanner(t, repo)
	writeRouteAllocatingPlanner(t, repo)
	// Deliberately NO reindex.

	agentOut, _ := run(t, testBin, repo, "agent", "validate")
	doctorOut, _ := run(t, testBin, repo, "doctor")

	agentOrphan := strings.Contains(agentOut, "[planner] is orphaned") ||
		strings.Contains(agentOut, "planner") && strings.Contains(agentOut, "orphaned")
	doctorOrphan := strings.Contains(doctorOut, "planner") && strings.Contains(doctorOut, "orphaned")

	if agentOrphan != doctorOrphan {
		t.Fatalf("agent validate and doctor must not disagree about the same allocation "+
			"on an unchanged tree (agent orphan=%v, doctor orphan=%v)\n"+
			"--- agent validate ---\n%s\n--- doctor ---\n%s",
			agentOrphan, doctorOrphan, agentOut, doctorOut)
	}
	if agentOrphan {
		t.Errorf("a node allocating agent=planner must not be reported orphaned:\n%s", agentOut)
	}
}

// TestValidateAndDoctorAgreeOnUnresolvedSkillWithoutReindex (AC1, AC7): the same
// property for `satelle workflow validate`, which shares the index-backed path.
// A workflow written to disk and not indexed must be seen by both.
func TestValidateAndDoctorAgreeOnUnresolvedSkillWithoutReindex(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// A route naming a gate skill that does not exist, written straight to disk.
	writeSpineFixture(t, repo, "", "", "",
		"in_progress|executor||never-authored-review|reviewer",
		"done||||")
	// Deliberately NO reindex.

	validateOut, _ := run(t, testBin, repo, "workflow", "validate")
	doctorOut, _ := run(t, testBin, repo, "doctor")

	inValidate := strings.Contains(validateOut, "never-authored-review")
	inDoctor := strings.Contains(doctorOut, "never-authored-review")
	if inValidate != inDoctor {
		t.Fatalf("validate and doctor must see the same authored files (validate=%v doctor=%v)\n"+
			"--- validate ---\n%s\n--- doctor ---\n%s", inValidate, inDoctor, validateOut, doctorOut)
	}
	if !inValidate {
		t.Errorf("a workflow written to disk but not indexed must still be checked:\n%s", validateOut)
	}
}

// TestValidateStillSeesEmbeddedDefaults (AC5) is the regression the naive fix
// would cause: swapping to the raw authored set alone would hide embedded
// default workflows, which really do govern a repo that has not overridden them.
// Allocation must judge the GOVERNING set (authored ∪ unshadowed embedded).
func TestValidateStillSeesEmbeddedDefaults(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// PRECONDITION, asserted rather than assumed: satelle ships sparse virtual
	// defaults, so a freshly-initialised repo has NO authored workflow on disk
	// (only the reserved README keep-file). This is what makes the test
	// discriminating — with a naive WorkflowDocs-only swap the authored set here
	// is empty, the allocation view would be empty too, and the assertions below
	// would fail. Note this repo does NOT call the materializeDefaultSolution
	// test helper, which would seed defaults onto disk and shadow the embedded
	// set, making the test vacuous.
	entries, err := os.ReadDir(filepath.Join(repo, ".satelle", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") && e.Name() != "README.md" {
			t.Fatalf("precondition broken: %s is an authored workflow on disk, so this test "+
				"would no longer prove embedded defaults are seen", e.Name())
		}
	}

	out := mustRun(t, testBin, repo, "workflow", "validate")
	if !strings.Contains(out, "Gate/node effective models:") {
		t.Fatalf("allocation view must still see embedded default workflows:\n%s", out)
	}
	// Name the embedded workflow explicitly: these nodes exist ONLY in the
	// embedded substrate, so seeing them proves the governing set (authored ∪
	// unshadowed embedded) is in use, not the authored set alone.
	if !strings.Contains(out, "NODE [done.md+step.md (*)]") {
		t.Errorf("allocations must come from the unshadowed EMBEDDED route:\n%s", out)
	}

	agentOut, err := run(t, testBin, repo, "agent", "validate")
	if err != nil {
		t.Fatalf("agent validate should pass on a fresh repo: %v\n%s", err, agentOut)
	}
	if !strings.Contains(agentOut, "NODE [done.md+step.md (*)]") {
		t.Errorf("agent validate must report node allocations from embedded defaults:\n%s", agentOut)
	}
	// A gate skill that resolves only as an EMBEDDED default must not be reported
	// unresolved — a disk-only resolver would wrongly flag every embedded gate.
	if strings.Contains(agentOut, "does not resolve in the substrate") {
		t.Errorf("embedded default skills must resolve:\n%s", agentOut)
	}
}

// TestDoctorStillWorksWithNoIndex (AC4): doctor is deliberately store-free so a
// broken index cannot stop it reporting. Exporting a helper for the CLI to share
// must not have changed that.
func TestDoctorStillWorksWithNoIndex(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	env := append(os.Environ(), "SATELLE_HOME="+home, "SATELLE_SERVER_ENDPOINT=none")
	runIn := func(args ...string) (string, error) {
		cmd := exec.Command(testBin, args...)
		cmd.Dir = repo
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := runIn("init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	writeAgentsWithPlanner(t, repo)
	writeRouteAllocatingPlanner(t, repo)
	if out, err := runIn("reindex"); err != nil {
		t.Fatalf("reindex: %v\n%s", err, out)
	}

	// DESTROY THE INDEX. The database is the doc index's home; removing it is the
	// "missing index" case. doctor is deliberately store-free — its checks read
	// authored files — so a repo whose index is gone must still get a full report.
	// (store.Open recreates an empty database on the next open, which is exactly
	// the empty-index case.)
	var removed int
	planes, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range planes {
		if !p.IsDir() {
			continue
		}
		db := filepath.Join(home, p.Name(), "satelle.db")
		if _, statErr := os.Stat(db); statErr == nil {
			if rmErr := os.Remove(db); rmErr != nil {
				t.Fatal(rmErr)
			}
			removed++
		}
	}
	if removed == 0 {
		t.Fatal("precondition broken: no database found to remove, so the no-index case is not exercised")
	}

	out, _ := runIn("doctor")
	if !strings.Contains(out, "HEALTHY") && !strings.Contains(out, "problem") {
		t.Fatalf("doctor must produce a full report with no index:\n%s", out)
	}
	// And it must still see what is on DISK — the allocation written above.
	if strings.Contains(out, "planner") && strings.Contains(out, "orphaned") {
		t.Errorf("doctor must see the on-disk allocation even with no index:\n%s", out)
	}
}
