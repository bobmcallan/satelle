//go:build integration

// Black-box coverage for sty_f4c1bd90: the embedded satelle-workflow-advisor
// skill — the semantic workflow review the in-loop agent runs after `satelle
// workflow validate` — seeds at init, validates as substrate, survives a
// rebase, and is discoverable from the workflows help topic.
package tests

import (
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
	for _, want := range []string{"Bash(satelle:*)", "EXIT edge", "pulls by id"} {
		if !strings.Contains(advisor, want) {
			t.Errorf("seeded advisor skill missing %q:\n%s", want, advisor)
		}
	}
}
