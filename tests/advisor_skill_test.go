//go:build integration

// Black-box coverage for advisory skills under virtual sparse defaults
// (sty_29e5a9a5 / sty_f4c1bd90): skills resolve virtually, can be materialised
// for edit, validate, and survive rebase redeploy.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdvisorSkillValidatesVirtuallyAndSurvivesMaterializeRebase(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Virtual: not on disk after init.
	if fileExists(filepath.Join(repo, ".satelle", "skills", "satelle-workflow-advisor.md")) {
		t.Fatal("init must not seed satelle-workflow-advisor")
	}
	// List shows it as default provenance.
	list := mustRun(t, testBin, repo, "substrate", "list", "--json")
	if !strings.Contains(list, "satelle-workflow-advisor") {
		t.Errorf("virtual advisor missing from substrate list:\n%s", list)
	}
	// Materialize for on-disk validate path.
	materializeDefault(t, repo, "skills", "satelle-workflow-advisor")
	mustRun(t, testBin, repo, "reindex")
	out := mustRun(t, testBin, repo, "skill", "validate", "satelle-workflow-advisor")
	if !strings.Contains(out, "PASS  skills/satelle-workflow-advisor") {
		t.Errorf("advisor skill should validate:\n%s", out)
	}

	mustRun(t, testBin, repo, "rebase", "--yes")
	if !fileExists(filepath.Join(repo, ".satelle", "skills", "satelle-workflow-advisor.md")) {
		t.Fatal("rebase did not redeploy satelle-workflow-advisor")
	}

	help := mustRun(t, testBin, repo, "help", "workflows")
	if !strings.Contains(help, "satelle-workflow-advisor") {
		t.Errorf("help workflows should reference the advisor skill:\n%s", help)
	}
}

func TestReviewerObjectiveAuditTaskSeedsSkillVirtual(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Task plane still seeds; skill is virtual.
	task := filepath.Join(repo, ".satelle", "tasks", "tsk_reviewer-objective-audit.md")
	if !fileExists(task) {
		t.Fatal("init did not seed tsk_reviewer-objective-audit task")
	}
	skill := filepath.Join(repo, ".satelle", "skills", "satelle-reviewer-objective-audit.md")
	if fileExists(skill) {
		t.Fatal("init must not seed satelle-reviewer-objective-audit skill")
	}
	materializeDefault(t, repo, "skills", "satelle-reviewer-objective-audit")
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

func TestContextAuditTaskSeedsSkillVirtual(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	task := filepath.Join(repo, ".satelle", "tasks", "tsk_context-audit.md")
	if !fileExists(task) {
		t.Fatal("init did not seed tsk_context-audit task")
	}
	skill := filepath.Join(repo, ".satelle", "skills", "satelle-context-audit.md")
	if fileExists(skill) {
		t.Fatal("init must not seed satelle-context-audit skill")
	}
	body, err := os.ReadFile(task)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "status: done") {
		t.Errorf("context-audit task must ship at done for re-run from done:\n%s", body)
	}
	materializeDefault(t, repo, "skills", "satelle-context-audit")
	mustRun(t, testBin, repo, "reindex")
	out := mustRun(t, testBin, repo, "skill", "validate", "satelle-context-audit")
	if !strings.Contains(out, "PASS  skills/satelle-context-audit") {
		t.Errorf("context-audit skill should validate:\n%s", out)
	}
}
