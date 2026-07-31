//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wfNamingMissingSkill is a structurally valid workflow whose exit gate names a
// skill that does not exist anywhere in the substrate — the mistake a first-time
// author makes when adopting a domain gate their repo does not ship.
func wfNamingMissingSkill(name string) string {
	// scope is required frontmatter. The executor node deliberately carries NO
	// @skill: prompt: an EXECUTOR-path skill that does not resolve is a hard
	// validation failure by design (AC7), and that is not what this fixture is
	// testing — only the REVIEWER gate is meant to be unresolvable.
	return "---\nname: " + name + "\ntype: workflow\nscope: project\ntags: [type:workflow]\n" +
		"applies_to: [\"nothing-selects-this\"]\n" +
		"description: fixture lifecycle whose exit gate names a skill that does not exist\n---\n\n" +
		"# fixture\n\n```dot\ndigraph w {\n" +
		"  backlog     [shape=Mdiamond]\n" +
		"  in_progress [agent=executor]\n" +
		"  done        [shape=Msquare]\n" +
		"  backlog     -> in_progress [agent=reviewer, prompt=\"@skill:construction-review\"]\n" +
		"  in_progress -> done\n" +
		"}\n```\n"
}

// TestNamedWorkflowValidateWarnsOnUnresolvedGateSkill (sty_d59ec6a9 AC1): the
// NAME-FILTERED form is the one an author runs while authoring, and the one the
// embedded satelle-workflow-advisor skill tells an agent to run. It used to skip
// the consistency check entirely, so a workflow whose gate skill does not exist
// validated clean and then advanced ungated at run time.
//
// It WARNs (AC4): a repo mid-authoring writes the workflow before its gate
// skills, so this must report without blocking — exit stays 0.
func TestNamedWorkflowValidateWarnsOnUnresolvedGateSkill(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	const name = "fixture-unresolved-gate"
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, name+".md"), []byte(wfNamingMissingSkill(name)), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex")

	out, err := run(t, testBin, repo, "workflow", "validate", name)
	if err != nil {
		t.Fatalf("named validate must not FAIL on an unresolved gate skill (a repo mid-authoring "+
			"writes the workflow before its skills): %v\n%s", err, out)
	}
	if !strings.Contains(out, "construction-review") {
		t.Errorf("named validate must name the unresolved gate skill:\n%s", out)
	}
	if !strings.Contains(out, "WARN") {
		t.Errorf("the report must be a WARN:\n%s", out)
	}
	if !strings.Contains(out, "UNGATED") {
		t.Errorf("the warning must name the CONSEQUENCE — that the edge advances ungated — "+
			"not only the missing file:\n%s", out)
	}
}

// TestWorkflowCreateWarnsOnUnresolvedGateSkill (AC2): create ran only the
// structure check, which deliberately does not require reviewer gates to
// resolve — so the file was written in silence. It now writes AND warns.
func TestWorkflowCreateWarnsOnUnresolvedGateSkill(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	const name = "fixture-created-unresolved"
	draft := filepath.Join(t.TempDir(), "draft.md")
	if err := os.WriteFile(draft, []byte(wfNamingMissingSkill(name)), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, testBin, repo, "workflow", "create", "--name", name, "--from", draft)
	if err != nil {
		t.Fatalf("create must still write the workflow, not refuse it: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".satelle", "workflows", name+".md")); statErr != nil {
		t.Fatalf("the workflow must be written: %v", statErr)
	}
	if !strings.Contains(out, "construction-review") {
		t.Errorf("create must name the unresolved gate skill:\n%s", out)
	}
	if !strings.Contains(out, "UNGATED") {
		t.Errorf("create must name the consequence:\n%s", out)
	}
}

// TestWholeSetValidateStillFailsOnUnresolvedGateSkill (AC3): the whole-set path
// answers a different question — is my substrate coherent as shipped — and keeps
// FAIL. The named form's WARN must not have softened it.
func TestWholeSetValidateStillFailsOnUnresolvedGateSkill(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	const name = "fixture-wholeset-unresolved"
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, name+".md"), []byte(wfNamingMissingSkill(name)), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex")

	out, err := run(t, testBin, repo, "workflow", "validate")
	if err == nil {
		t.Fatalf("bare (whole-set) validate must still FAIL on an unresolved gate skill:\n%s", out)
	}
	if !strings.Contains(out, "FAIL") || !strings.Contains(out, "construction-review") {
		t.Errorf("whole-set output must still report it as a FAIL naming the skill:\n%s", out)
	}
}

// TestUngatedAdvanceIsRecordedOnTheLedger (AC5, AC6): a transition whose gate
// skill does not resolve still advances — fail-open is deliberate — but the
// trail now says it was not judged, instead of looking like an edge that never
// carried a gate.
func TestUngatedAdvanceIsRecordedOnTheLedger(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	const name = "fixture-ungated-advance"
	wf := "---\nname: " + name + "\ntype: workflow\nscope: project\ntags: [type:workflow]\n" +
		"applies_to: [\"ungated-fixture\"]\n" +
		"description: fixture lifecycle whose intake gate names a skill that does not exist\n---\n\n" +
		"# fixture\n\n```dot\ndigraph w {\n" +
		"  backlog     [shape=Mdiamond]\n" +
		"  in_progress [agent=executor]\n" +
		"  done        [shape=Msquare]\n" +
		"  backlog     -> in_progress [agent=reviewer, prompt=\"@skill:construction-review\"]\n" +
		"  in_progress -> done\n" +
		"}\n```\n"
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, name+".md"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create",
		"--title", "fixture story for the ungated advance",
		"--category", "ungated-fixture")
	id := extractStoryID(t, out)

	// The transition ENACTS despite the declared gate being unresolvable.
	if out, err := run(t, testBin, repo, "story", "set", id, "--status", "in_progress"); err != nil {
		t.Fatalf("fail-open must be preserved — the transition must still enact: %v\n%s", err, out)
	}
	got := mustRun(t, testBin, repo, "story", "get", id)
	if !strings.Contains(got, `"status": "in_progress"`) {
		t.Fatalf("story should have advanced:\n%s", got)
	}

	// …and the ledger says it was not judged.
	led := mustRun(t, testBin, repo, "ledger", "list", "--story", id)
	if !strings.Contains(led, "gate_skipped") {
		t.Errorf("an ungated advance must be recorded as gate_skipped:\n%s", led)
	}
	if !strings.Contains(led, "construction-review") {
		t.Errorf("the ledger row must name the skill that did not resolve:\n%s", led)
	}
	if !strings.Contains(led, "UNGATED") {
		t.Errorf("the ledger row must say the advance was ungated:\n%s", led)
	}
}

// extractStoryID pulls the id out of `satelle story create` JSON output.
func extractStoryID(t *testing.T, out string) string {
	t.Helper()
	const key = `"id": "`
	i := strings.Index(out, key)
	if i < 0 {
		t.Fatalf("no story id in:\n%s", out)
	}
	rest := out[i+len(key):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("malformed story id in:\n%s", out)
	}
	return rest[:j]
}
