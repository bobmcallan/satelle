//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// routeNamingMissingSkill is a structurally valid route whose entry gate names a
// skill that does not exist anywhere in the substrate — the mistake a first-time
// author makes when adopting a domain gate their repo does not ship.
//
// The executor step deliberately carries NO skills: an EXECUTOR-path skill that
// does not resolve is a hard validation failure by design (AC7), and that is not
// what this fixture is testing — only the REVIEWER gate is meant to be
// unresolvable.
func routeNamingMissingSkill() (done, step string) {
	return spineFixture("", "", "",
		"in_progress|executor||construction-review|reviewer",
		"done||||")
}

// writeRouteNamingMissingSkill lands that route in a repo.
func writeRouteNamingMissingSkill(t *testing.T, repo string) {
	t.Helper()
	done, step := routeNamingMissingSkill()
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.md"), done)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.md"), step)
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

	writeRouteNamingMissingSkill(t, repo)
	mustRun(t, testBin, repo, "reindex")

	// The named form narrows to one authored file; the half that names the gate
	// is step.md.
	out, err := run(t, testBin, repo, "workflow", "validate", "step")
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

	_, step := routeNamingMissingSkill()
	draft := filepath.Join(t.TempDir(), "draft.md")
	if err := os.WriteFile(draft, []byte(step), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, testBin, repo, "workflow", "create", "--name", "step", "--from", draft, "--force")
	if err != nil {
		t.Fatalf("create must still write the route half, not refuse it: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".satelle", "workflows", "step.md")); statErr != nil {
		t.Fatalf("the route half must be written: %v", statErr)
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

	writeRouteNamingMissingSkill(t, repo)
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

	writeRouteNamingMissingSkill(t, repo)
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
