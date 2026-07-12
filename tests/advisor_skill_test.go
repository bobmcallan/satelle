//go:build integration

// Black-box coverage for sty_f4c1bd90: the embedded satelle-workflow-advisor
// skill — the semantic workflow review the in-loop agent runs after `satelle
// workflow validate` — seeds at init, validates as substrate, survives a
// rebase, and is discoverable from the workflows help topic.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdvisorSkillSeedsValidatesAndSurvivesRebase(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	if !fileExists(filepath.Join(repo, ".satelle", "skills", "satelle-workflow-advisor.md")) {
		t.Fatal("init did not seed satelle-workflow-advisor")
	}
	mustRun(t, testBin, repo, "reindex")
	out := mustRun(t, testBin, repo, "skill", "validate", "satelle-workflow-advisor")
	if !strings.Contains(out, "PASS  skills/satelle-workflow-advisor") {
		t.Errorf("advisor skill should validate:\n%s", out)
	}

	// A rebase (backup + wipe + redeploy) must redeploy it — it is referenced by
	// no workflow, so the default-solution deploy alone would drop it.
	mustRun(t, testBin, repo, "rebase", "--yes")
	if !fileExists(filepath.Join(repo, ".satelle", "skills", "satelle-workflow-advisor.md")) {
		t.Fatal("rebase did not redeploy satelle-workflow-advisor")
	}

	// Discoverable: the workflows help topic points the agent at the review.
	help := mustRun(t, testBin, repo, "help", "workflows")
	if !strings.Contains(help, "satelle-workflow-advisor") {
		t.Errorf("help workflows should reference the advisor skill:\n%s", help)
	}
}

// TestReviewerObjectiveAuditSeedsOnInit: advisory skill + paired task are not
// referenced by any workflow, so they land via advisorySkills / materializeTasks.
func TestReviewerObjectiveAuditSeedsOnInit(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	skill := filepath.Join(repo, ".satelle", "skills", "satelle-reviewer-objective-audit.md")
	task := filepath.Join(repo, ".satelle", "tasks", "tsk_reviewer-objective-audit.md")
	if !fileExists(skill) {
		t.Fatal("init did not seed satelle-reviewer-objective-audit skill")
	}
	if !fileExists(task) {
		t.Fatal("init did not seed tsk_reviewer-objective-audit task")
	}
	mustRun(t, testBin, repo, "reindex")
	out := mustRun(t, testBin, repo, "skill", "validate", "satelle-reviewer-objective-audit")
	if !strings.Contains(out, "PASS  skills/satelle-reviewer-objective-audit") {
		t.Errorf("reviewer-objective-audit skill should validate:\n%s", out)
	}
	mustRun(t, testBin, repo, "rebase", "--yes")
	if !fileExists(skill) {
		t.Fatal("rebase did not redeploy satelle-reviewer-objective-audit")
	}
	if !fileExists(task) {
		t.Fatal("rebase did not redeploy tsk_reviewer-objective-audit")
	}
}

// TestContextAuditSeedsOnInit: context-audit skill + task land via advisorySkills
// / materializeTasks (epic order:8), validate, and survive rebase.
func TestContextAuditSeedsOnInit(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	skill := filepath.Join(repo, ".satelle", "skills", "satelle-context-audit.md")
	task := filepath.Join(repo, ".satelle", "tasks", "tsk_context-audit.md")
	if !fileExists(skill) {
		t.Fatal("init did not seed satelle-context-audit skill")
	}
	if !fileExists(task) {
		t.Fatal("init did not seed tsk_context-audit task")
	}
	body, err := os.ReadFile(task)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "status: done") {
		t.Errorf("context-audit task must ship at done for re-run from done:\n%s", body)
	}
	mustRun(t, testBin, repo, "reindex")
	out := mustRun(t, testBin, repo, "skill", "validate", "satelle-context-audit")
	if !strings.Contains(out, "PASS  skills/satelle-context-audit") {
		t.Errorf("context-audit skill should validate:\n%s", out)
	}
	// Fresh execution under the done header resolves via the task workflow.
	stubReviewerAccept(t, repo)
	eid := extractID(mustRun(t, testBin, repo, "execution", "create", "--parent", "tsk_context-audit",
		"--title", "Context audit run", "--body", "ACTION: audit session context. VERIFICATION: report written."), "exe_")
	if eid == "" {
		t.Fatal("no execution id under tsk_context-audit")
	}
	mustRun(t, testBin, repo, "execution", "set", eid, "--status", "in_progress")
	mustRun(t, testBin, repo, "execution", "set", eid, "--status", "done")

	mustRun(t, testBin, repo, "rebase", "--yes")
	if !fileExists(skill) {
		t.Fatal("rebase did not redeploy satelle-context-audit")
	}
	if !fileExists(task) {
		t.Fatal("rebase did not redeploy tsk_context-audit")
	}
}

// TestAgentDispatchContractDiscoverableFromInit (sty_75ab9246, AC4): an agent in a
// binary-only repo can answer "how does a named agent receive its instructions and
// what makes a flip sufficient" from the deployed substrate + help alone — the
// dispatch contract ships as a help topic and the seeded advisor carries the
// pull-context grant + sequencing preconditions, with no Go source needed.
func TestAgentDispatchContractDiscoverableFromInit(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// The contract topic renders in a fresh repo and teaches the whole flow.
	topic := mustRun(t, testBin, repo, "help", "agent-dispatch")
	for _, want := range []string{
		"agents.toml", "@skill:", "Bash(satelle:*)",
		"satelle story get <id>", "satelle ledger list --story <id>",
		"self-sufficient", "EXIT edge", "{story, from, to, review_skill}",
	} {
		if !strings.Contains(topic, want) {
			t.Errorf("help agent-dispatch missing %q (contract must be answerable from deployed help):\n%s", want, topic)
		}
	}

	// The seeded advisor skill carries the widened preconditions (pull-context
	// grant + entry-dispatch/exit-review sequencing).
	mustRun(t, testBin, repo, "reindex")
	advisor := mustRun(t, testBin, repo, "doc", "get", "skills", "satelle-workflow-advisor")
	for _, want := range []string{"Bash(satelle:*)", "EXIT edge", "pulls by id",
		".claude/agents", "anti-pattern"} { // sty_fd4b1cd4: process agents live in the agents layer
		if !strings.Contains(advisor, want) {
			t.Errorf("seeded advisor skill missing %q:\n%s", want, advisor)
		}
	}
}
