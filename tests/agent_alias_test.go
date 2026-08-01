//go:build integration

package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentKeywordParsesEndToEnd proves the agent: key is honoured as the
// PERFORMER end-to-end through the real binary (sty_536f9960): the deterministic
// workflow structure check only collects an executor-path skill from a step it
// recognises AS an executor, so `satelle workflow validate` can only report the
// unresolved skill if `agent: executor` was parsed as the performer.
func TestAgentKeywordParsesEndToEnd(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeSpineFixture(t, repo, "", "", "",
		"in_progress|executor|agent-alias-missing-skill||",
		"done||||")
	mustRun(t, testBin, repo, "reindex")

	out, err := run(t, testBin, repo, "workflow", "validate")
	if err == nil {
		t.Fatalf("validate should fail: the agent: executor step names an unresolved skill:\n%s", out)
	}
	if !strings.Contains(out, "agent-alias-missing-skill") {
		t.Errorf("validate should report the unresolved executor-path skill (proving agent: parsed as executor):\n%s", out)
	}
}

// TestAgentsTomlBootsEndToEnd proves the agents.toml back-compat loader end-to-end
// (sty_536f9960): with ONLY the canonical agents.toml present (the legacy
// actors.toml removed), the real binary boots, indexes, and reports status cleanly
// — applyActorGrants resolves the [reviewer] binding from agents.toml on store
// open. It is the agents.toml counterpart to TestReviewerModelActorsBoots, proving
// the binary no longer depends on the actors.toml filename.
func TestAgentsTomlBootsEndToEnd(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// init scaffolds the canonical agents.toml; overwrite it with a reviewer-model
	// binding. With no legacy actors.toml present, a loader that ignored agents.toml
	// would resolve no binding at all.
	writeFile(t, filepath.Join(repo, ".satelle", "agents.toml"), "[reviewer]\nmodel = \"sonnet\"\n")
	mustRun(t, testBin, repo, "reindex")
	out := mustRun(t, testBin, repo, "status")
	if !strings.Contains(out, "repo root") {
		t.Errorf("status should boot cleanly with only agents.toml present:\n%s", out)
	}
}

// The retired actor= keyword had its own end-to-end rejection test
// (TestValidateRejectsActorKeyword, sty_7db2ed7d). It retires with the DOT front
// end that carried the keyword: the route grammar has one performer key, `agent:`,
// and no `actor:` to deprecate (sty_d953c5d8).

// TestReindexWarnsActorsToml proves reindex flags the retired actors.toml
// filename (sty_7db2ed7d): a repo still carrying it is silently on defaults, so
// reindex WARNS telling the operator to rename it to agents.toml (the legacy
// filename is no longer loaded).
func TestReindexWarnsActorsToml(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// init scaffolds agents.toml; drop a legacy actors.toml beside it.
	writeFile(t, filepath.Join(repo, ".satelle", "actors.toml"), "[reviewer]\nmodel = \"sonnet\"\n")

	out := mustRun(t, testBin, repo, "reindex") // a pass-through — warns, does not fail
	if !strings.Contains(out, "actors.toml") || !strings.Contains(out, "agents.toml") {
		t.Errorf("reindex should warn about the legacy actors.toml and name the agents.toml fix:\n%s", out)
	}
}
